package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

// GET /v1/clock/health: publish success, the clock's own telemetry, and
// whether newer awtrix-ng firmware exists — without scraping /metrics or
// needing the token /v1/device/stats sits behind. See dashboard_http.go for
// the wire conventions.

// clockProbeTTL bounds how often the open health endpoint may reach the clock:
// however many viewers poll it, the clock sees one GET per window.
const clockProbeTTL = 30 * time.Second

// clockProbeTimeout bounds one health probe. The clock's Wi-Fi is lossy; a
// dashboard would rather show "unreachable" than hang.
const clockProbeTimeout = 3 * time.Second

// ngReleasesURL is awtrix-ng's latest-release API. main() wires it into
// App.firmware; tests leave it unset so they never touch the network.
const ngReleasesURL = "https://api.github.com/repos/Blueforcer/awtrix-ng/releases/latest"

const (
	firmwareCheckTTL     = 6 * time.Hour    // a release lookup stays good this long
	firmwareCheckRetry   = 30 * time.Minute // after a failed lookup, wait this long
	firmwareCheckTimeout = 4 * time.Second
)

// clockProbeCache holds the last GET /api/v1/device result. The zero value is
// ready to use.
type clockProbeCache struct {
	mu   sync.Mutex // protects all fields; held across a probe to single-flight it
	at   time.Time
	base string // the clock URL probed; a different URL invalidates the entry
	dev  clockDeviceOut
}

// publishWindow counts publish outcomes in hourly buckets over the last 24h.
// The zero value is ready to use.
type publishWindow struct {
	mu      sync.Mutex // protects buckets
	buckets [24]publishBucket
}

type publishBucket struct {
	hour     int64 // unix hour this bucket holds; stale buckets are reset on reuse
	ok, fail int64
}

func (p *publishWindow) add(at time.Time, ok bool) {
	h := at.Unix() / 3600
	p.mu.Lock()
	defer p.mu.Unlock()
	b := &p.buckets[h%24]
	if b.hour != h {
		*b = publishBucket{hour: h}
	}
	if ok {
		b.ok++
	} else {
		b.fail++
	}
}

// last24h sums the buckets for the 24 hours ending at now.
func (p *publishWindow) last24h(now time.Time) (ok, fail int64) {
	h := now.Unix() / 3600
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range p.buckets {
		if b.hour > h-24 && b.hour <= h {
			ok += b.ok
			fail += b.fail
		}
	}
	return ok, fail
}

// firmwareCheck caches the latest awtrix-ng release version. Lookups run in
// a background goroutine so /v1/clock/health never waits on GitHub. The zero
// value is ready to use and disabled (url empty).
type firmwareCheck struct {
	mu       sync.Mutex // protects the fields below
	url      string     // "" disables the lookup (EMBER_FIRMWARE_CHECK=0, tests)
	client   *http.Client
	at       time.Time // last attempt started
	ok       bool      // last attempt succeeded
	latest   string    // "1.1.2", from the release tag
	inFlight bool      // a refresh goroutine is running

	wg sync.WaitGroup // tracks the refresh goroutine; tests Wait on it
}

// cached returns the newest known firmware version ("" when disabled or not
// yet looked up) and, when the cached answer is due for renewal, starts one
// background refresh. It never blocks on the network. Failures keep the
// previous answer, log once per attempt (at most every firmwareCheckRetry),
// and retry after that window.
func (f *firmwareCheck) cached(now time.Time, logger *slog.Logger) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.url == "" {
		return ""
	}
	wait := firmwareCheckRetry
	if f.ok {
		wait = firmwareCheckTTL
	}
	if !f.inFlight && (f.at.IsZero() || now.Sub(f.at) >= wait) {
		f.inFlight = true
		f.at = now
		f.wg.Add(1)
		go f.refresh(logger)
	}
	return f.latest
}

// refresh performs one lookup and records the result. The goroutine is bounded
// by firmwareCheckTimeout.
func (f *firmwareCheck) refresh(logger *slog.Logger) {
	defer f.wg.Done()
	v, err := f.fetch(context.Background())
	f.mu.Lock()
	f.inFlight = false
	f.ok = err == nil
	if err == nil {
		f.latest = v
	}
	f.mu.Unlock()
	if err != nil && logger != nil {
		logger.Warn("firmware release lookup failed", "err", err, "retry_in", firmwareCheckRetry.String())
	}
}

func (f *firmwareCheck) fetch(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, firmwareCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ember (github.com/tarakanof/ember)") // required by the GitHub API
	cl := f.client
	if cl == nil {
		cl = &http.Client{Timeout: firmwareCheckTimeout}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release lookup: %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return "", fmt.Errorf("decode release: %w", err)
	}
	v := strings.TrimPrefix(rel.TagName, "v")
	if _, ok := parseVersion(v); !ok {
		return "", fmt.Errorf("unexpected release tag %q", rel.TagName)
	}
	return v, nil
}

// parseVersion splits "1.1.2" into its numeric parts.
func parseVersion(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// versionNewer reports whether latest is a higher version than installed; ok is
// false when either doesn't parse.
func versionNewer(latest, installed string) (newer, ok bool) {
	l, ok1 := parseVersion(latest)
	i, ok2 := parseVersion(installed)
	if !ok1 || !ok2 {
		return false, false
	}
	for k := 0; k < max(len(l), len(i)); k++ {
		var a, b int
		if k < len(l) {
			a = l[k]
		}
		if k < len(i) {
			b = i[k]
		}
		if a != b {
			return a > b, true
		}
	}
	return false, true
}

// clockDeviceOut is the clock's own telemetry. Pointers are null when the
// clock is unreachable or its firmware doesn't report the field.
type clockDeviceOut struct {
	Reachable        bool      `json:"reachable"`
	CheckedAt        time.Time `json:"checked_at"`
	Firmware         *string   `json:"firmware"`
	CurrentApp       *string   `json:"current_app"`
	UptimeSec        *int64    `json:"uptime_sec"`
	FreeHeapBytes    *int64    `json:"free_heap_bytes"`
	MinFreeHeapBytes *int64    `json:"min_free_heap_bytes"`
	WifiRSSIDbm      *int      `json:"wifi_rssi_dbm"`
	WifiConnects     *int      `json:"wifi_connects"` // (re)connects since boot; >1 means the link dropped
	ResetReason      *string   `json:"reset_reason"`
	FPS              *float64  `json:"fps"`
	MatrixPower      *bool     `json:"matrix_power"`
	BatteryPercent   *float64  `json:"battery_percent"`
	LowBattery       *bool     `json:"low_battery"`
	TemperatureC     *float64  `json:"temperature_c"`
	HumidityPercent  *float64  `json:"humidity_percent"`
}

// publishHealthOut is the server→clock push record.
type publishHealthOut struct {
	CountingSince time.Time `json:"counting_since"` // server start; every counter resets on restart
	OK24h         int64     `json:"ok_24h"`
	Fail24h       int64     `json:"fail_24h"`
	// SuccessRatio24h is ok/(ok+fail) over the last 24h, 0..1; null without
	// publishes in that window.
	SuccessRatio24h *float64   `json:"success_ratio_24h"`
	OKTotal         int64      `json:"ok_total"`
	FailTotal       int64      `json:"fail_total"`
	RetriesTotal    int64      `json:"retries_total"` // lost first attempts that a retry recovered
	LastAt          *time.Time `json:"last_at"`
	LastOK          bool       `json:"last_ok"`
}

// clockHealthOut is the GET /v1/clock/health response.
type clockHealthOut struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Publish     publishHealthOut `json:"publish"`
	Device      *clockDeviceOut  `json:"device"` // null when no clock is configured
	// LatestFirmware is the newest awtrix-ng release ("1.1.2"), looked up on
	// GitHub in the background at most every 6h; null when unknown (the first
	// request after start, offline, rate-limited, or EMBER_FIRMWARE_CHECK=0).
	LatestFirmware *string `json:"latest_firmware"`
	// UpdateAvailable compares LatestFirmware with device.firmware; null when
	// either is unknown.
	UpdateAvailable *bool `json:"update_available"`
}

// clockDeviceWire decodes the subset of awtrix-ng's GET /api/v1/device the
// health view needs. Everything else in that payload (IP, SSID host, UID,
// hostname) is deliberately dropped: this endpoint is unauthenticated.
type clockDeviceWire struct {
	Version     string   `json:"version"`
	CurrentApp  string   `json:"currentApp"`
	WifiRSSI    *int     `json:"wifiRssi"`
	Uptime      *int64   `json:"uptimeSeconds"`
	FreeHeap    *int64   `json:"freeHeapBytes"`
	MinFreeHeap *int64   `json:"minFreeHeapBytes"`
	ResetReason string   `json:"resetReason"`
	FPS         *float64 `json:"fps"`
	MatrixPower *bool    `json:"matrixPower"`
	Battery     *float64 `json:"batteryPercent"`
	LowBattery  *bool    `json:"lowBattery"`
	Temperature *float64 `json:"temperature"`
	Humidity    *float64 `json:"humidity"`
	WiFi        struct {
		Connects *int `json:"connects"`
	} `json:"wifi"`
}

// probeClockHealth returns the cached device telemetry, refreshing it from the
// clock when older than clockProbeTTL. nil means no clock is configured. The
// probe runs detached from ctx's cancellation: a viewer that disconnects
// mid-probe must not cache "unreachable" for everyone else.
func (a *App) probeClockHealth(ctx context.Context, now time.Time) *clockDeviceOut {
	base := a.cfg.Load().AWTRIX.HTTPBaseURL
	if base == "" {
		return nil
	}
	c := &a.clockProbe
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.base == base && !c.at.IsZero() && now.Sub(c.at) < clockProbeTTL {
		dev := c.dev
		return &dev
	}

	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clockProbeTimeout)
	defer cancel()
	dev := clockDeviceOut{CheckedAt: now}
	reply, err := a.clock.raw(pctx, (*awtrix.Client).RawDevice)
	if err == nil && reply.Status == http.StatusOK {
		dev.Reachable = true
		var raw clockDeviceWire
		if json.Unmarshal(reply.Body, &raw) == nil {
			dev.Firmware = optString(raw.Version)
			dev.CurrentApp = optString(raw.CurrentApp)
			dev.UptimeSec = raw.Uptime
			dev.FreeHeapBytes = raw.FreeHeap
			dev.MinFreeHeapBytes = raw.MinFreeHeap
			dev.WifiRSSIDbm = raw.WifiRSSI
			dev.WifiConnects = raw.WiFi.Connects
			dev.ResetReason = optString(raw.ResetReason)
			dev.FPS = raw.FPS
			dev.MatrixPower = raw.MatrixPower
			dev.BatteryPercent = raw.Battery
			dev.LowBattery = raw.LowBattery
			dev.TemperatureC = raw.Temperature
			dev.HumidityPercent = raw.Humidity
		}
	}
	c.at, c.base, c.dev = now, base, dev
	return &dev
}

// buildClockHealth assembles GET /v1/clock/health as of now.
func (a *App) buildClockHealth(ctx context.Context, now time.Time) clockHealthOut {
	loc := now.Location()
	a.mu.Lock()
	lastAt, lastOK := a.lastPublishAt, a.lastPublishOK
	a.mu.Unlock()

	ok24, fail24 := a.publishWindow.last24h(now)
	pub := publishHealthOut{
		CountingSince: wireTime(a.startedAt, loc),
		OK24h:         ok24,
		Fail24h:       fail24,
		OKTotal:       a.metrics.publishTotalOK.Load(),
		FailTotal:     a.metrics.publishTotalFail.Load(),
		RetriesTotal:  a.metrics.publishRetries.Load(),
		LastAt:        wireTimePtr(lastAt, loc),
		LastOK:        lastOK,
	}
	if n := ok24 + fail24; n > 0 {
		ratio := float64(ok24) / float64(n)
		pub.SuccessRatio24h = &ratio
	}

	out := clockHealthOut{GeneratedAt: wireTime(now, loc), Publish: pub}
	if dev := a.probeClockHealth(ctx, now); dev != nil {
		d := *dev
		d.CheckedAt = wireTime(d.CheckedAt, loc)
		out.Device = &d
	}
	out.LatestFirmware = optString(a.firmware.cached(now, a.logger))
	if out.LatestFirmware != nil && out.Device != nil && out.Device.Firmware != nil {
		if newer, ok := versionNewer(*out.LatestFirmware, *out.Device.Firmware); ok {
			out.UpdateAvailable = &newer
		}
	}
	return out
}

// handleClockHealth serves GET /v1/clock/health.
func (a *App) handleClockHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildClockHealth(r.Context(), time.Now()))
}
