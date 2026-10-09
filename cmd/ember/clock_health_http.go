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
	"github.com/tarakanof/ember/internal/discovery"
)

const clockProbeTTL = 30 * time.Second

const clockProbeTimeout = 2500 * time.Millisecond

const ngReleasesURL = "https://api.github.com/repos/Blueforcer/awtrix-ng/releases/latest"

const (
	firmwareCheckTTL     = 6 * time.Hour
	firmwareCheckRetry   = 30 * time.Minute
	firmwareCheckTimeout = 4 * time.Second
)

type clockProbeCache struct {
	mu       sync.Mutex
	at       time.Time
	base     string
	dev      clockDeviceOut
	inflight chan struct{}
}

type publishWindow struct {
	mu      sync.Mutex
	buckets [24]publishBucket
}

type publishBucket struct {
	hour     int64
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

type firmwareCheck struct {
	mu       sync.Mutex
	url      string
	client   *http.Client
	at       time.Time
	ok       bool
	latest   string
	inFlight bool

	wg sync.WaitGroup
}

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
	req.Header.Set("User-Agent", "ember (github.com/tarakanof/ember)")
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

type clockDeviceOut struct {
	Reachable        bool      `json:"reachable"`
	CheckedAt        time.Time `json:"checked_at"`
	Firmware         *string   `json:"firmware"`
	CurrentApp       *string   `json:"current_app"`
	UptimeSec        *int64    `json:"uptime_sec"`
	FreeHeapBytes    *int64    `json:"free_heap_bytes"`
	MinFreeHeapBytes *int64    `json:"min_free_heap_bytes"`
	WifiRSSIDbm      *int      `json:"wifi_rssi_dbm"`
	WifiConnects     *int      `json:"wifi_connects"`
	ResetReason      *string   `json:"reset_reason"`
	FPS              *float64  `json:"fps"`
	MatrixPower      *bool     `json:"matrix_power"`
	BatteryPercent   *float64  `json:"battery_percent"`
	LowBattery       *bool     `json:"low_battery"`
	TemperatureC     *float64  `json:"temperature_c"`
	HumidityPercent  *float64  `json:"humidity_percent"`

	lightLevel *float64
	ip         string
	uid        string
}

type publishHealthOut struct {
	CountingSince   time.Time  `json:"counting_since"`
	OK24h           int64      `json:"ok_24h"`
	Fail24h         int64      `json:"fail_24h"`
	SuccessRatio24h *float64   `json:"success_ratio_24h"`
	OKTotal         int64      `json:"ok_total"`
	FailTotal       int64      `json:"fail_total"`
	RetriesTotal    int64      `json:"retries_total"`
	LastAt          *time.Time `json:"last_at"`
	LastOK          bool       `json:"last_ok"`
}

type clockHealthOut struct {
	GeneratedAt     time.Time        `json:"generated_at"`
	Publish         publishHealthOut `json:"publish"`
	Disabled        bool             `json:"disabled,omitempty"`
	Device          *clockDeviceOut  `json:"device"`
	LatestFirmware  *string          `json:"latest_firmware"`
	UpdateAvailable *bool            `json:"update_available"`
}

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
	LightLevel  *float64 `json:"lightLevel"`
	IPAddress   string   `json:"ipAddress"`
	UID         string   `json:"uid"`
	BoardType   string   `json:"boardType"`
	WiFi        struct {
		Connects *int `json:"connects"`
	} `json:"wifi"`
}

func (a *App) probeClockHealth(ctx context.Context, now time.Time) *clockDeviceOut {
	return a.probeClockHealthWithin(ctx, now, clockProbeTTL)
}

func (a *App) probeClockHealthWithin(ctx context.Context, now time.Time, maxAge time.Duration) *clockDeviceOut {
	base := a.cfg.Load().effectiveClockURL()
	if base == "" || clockDisabled() {
		return nil
	}
	c := &a.clockProbe
	c.mu.Lock()
	for {
		if c.base == base && !c.at.IsZero() && now.Sub(c.at) < maxAge {
			dev := c.dev
			c.mu.Unlock()
			return &dev
		}
		if c.inflight == nil {
			break
		}
		if c.base == base && !c.at.IsZero() {
			dev := c.dev
			c.mu.Unlock()
			return &dev
		}
		wait := c.inflight
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
	}
	done := make(chan struct{})
	c.inflight = done
	c.mu.Unlock()
	defer close(done)

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
			dev.lightLevel = raw.LightLevel
			dev.ip = raw.IPAddress
			if raw.BoardType == discovery.NGBoardType {
				dev.uid = raw.UID
			}
		}
	}
	c.mu.Lock()
	c.at, c.base, c.dev, c.inflight = now, base, dev, nil
	c.mu.Unlock()
	a.recordClockProbe(now, base, dev)
	if dev.uid != "" && a.cfg.Load().effectiveClockURL() == base {
		seen := &clockSeen{IP: dev.ip, RSSI: dev.WifiRSSIDbm, UptimeS: dev.UptimeSec}
		if dev.Firmware != nil {
			seen.FW = *dev.Firmware
		}
		a.observeClock(dev.uid, seen)
	}
	return &dev
}

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

	out := clockHealthOut{GeneratedAt: wireTime(now, loc), Publish: pub, Disabled: clockDisabled()}
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

func (a *App) handleClockHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildClockHealth(r.Context(), time.Now()))
}
