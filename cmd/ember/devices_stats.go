package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	// knobStatsLiveCap holds 10 min of 5 s live samples.
	knobStatsLiveCap = int(statsLiveWindow / (5 * time.Second))
	// knobLiveMax caps one POST /stats/live; the app extends it while its
	// dashboard is open.
	knobLiveMax     = 10 * time.Minute
	knobLiveDefault = 180 * time.Second
	// knobOnlineWindow is two missed 60 s checkins.
	knobOnlineWindow = 150 * time.Second
	knobMaxCores     = 8
)

var knobResetReasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,15}$`)

// knobStatsReport is a checkin's optional "stats" object: deltas and
// averages over period_ms, plus gauges. Every field is optional; see
// ARCHITECTURE "Knob diagnostics" for which level sends which.
type knobStatsReport struct {
	PeriodMS        int64     `json:"period_ms"`
	CPUPct          []float64 `json:"cpu_pct"`
	HeapInternalMin *int64    `json:"heap_internal_min"`
	PSRAMFree       *int64    `json:"psram_free"`
	PSRAMMin        *int64    `json:"psram_min"`
	PSRAMLargest    *int64    `json:"psram_largest"`
	TempC           *float64  `json:"temp_c"`
	ResetReason     string    `json:"reset_reason"`
	ReqOK           *int64    `json:"req_ok"`
	ReqFail         *int64    `json:"req_fail"`
	ReqMSAvg        *float64  `json:"req_ms_avg"`
	ReqMSMax        *int64    `json:"req_ms_max"`
	FPS             *float64  `json:"fps"`
	FrameMSAvg      *float64  `json:"frame_ms_avg"`
	FrameMSMax      *int64    `json:"frame_ms_max"`
}

func (s *knobStatsReport) validate() error {
	if s.PeriodMS != 0 && (s.PeriodMS < 1 || s.PeriodMS > 3_600_000) {
		return fmt.Errorf("period_ms %d out of range", s.PeriodMS)
	}
	if len(s.CPUPct) > knobMaxCores {
		return fmt.Errorf("cpu_pct has %d cores, max %d", len(s.CPUPct), knobMaxCores)
	}
	for _, c := range s.CPUPct {
		if c < 0 || c > 100 || math.IsNaN(c) {
			return fmt.Errorf("cpu_pct %v out of range", c)
		}
	}
	for name, v := range map[string]*int64{
		"heap_internal_min": s.HeapInternalMin, "psram_free": s.PSRAMFree, "psram_min": s.PSRAMMin,
		"psram_largest": s.PSRAMLargest, "req_ok": s.ReqOK, "req_fail": s.ReqFail,
		"req_ms_max": s.ReqMSMax, "frame_ms_max": s.FrameMSMax,
	} {
		if v != nil && *v < 0 {
			return fmt.Errorf("%s %d is negative", name, *v)
		}
	}
	for name, v := range map[string]*float64{"req_ms_avg": s.ReqMSAvg, "fps": s.FPS, "frame_ms_avg": s.FrameMSAvg} {
		if v != nil && (*v < 0 || *v > 1e6) {
			return fmt.Errorf("%s %v out of range", name, *v)
		}
	}
	if s.TempC != nil && (*s.TempC < -40 || *s.TempC > 150) {
		return fmt.Errorf("temp_c %v out of range", *s.TempC)
	}
	if s.ResetReason != "" && !knobResetReasonPattern.MatchString(s.ResetReason) {
		return fmt.Errorf("reset_reason %q must match %s", s.ResetReason, knobResetReasonPattern)
	}
	return nil
}

// forLevel keeps what diagnostics level diag asks for: nothing at off, no
// request or rendering fields at basic. The knob may lag a level change by
// a checkin, so the server, not the firmware, has the last word.
func (s *knobStatsReport) forLevel(diag string) *knobStatsReport {
	if s == nil || diag == knobDiagOff {
		return nil
	}
	if diag == knobDiagFull {
		return s
	}
	c := *s
	c.ReqOK, c.ReqFail, c.ReqMSAvg, c.ReqMSMax = nil, nil, nil, nil
	c.FPS, c.FrameMSAvg, c.FrameMSMax = nil, nil, nil
	return &c
}

// knobSample is one stored point and its wire shape in GET .../stats: units
// in keys, null when the knob didn't report it (level too low, or no PSRAM).
type knobSample struct {
	T                     time.Time `json:"t"`
	UptimeSec             *int64    `json:"uptime_sec"`
	RSSIDBm               *int      `json:"rssi_dbm"`
	CPUPercent            []float64 `json:"cpu_percent"`
	HeapInternalFreeBytes *int64    `json:"heap_internal_free_bytes"`
	HeapInternalMinBytes  *int64    `json:"heap_internal_min_bytes"`
	HeapInternalLargest   *int64    `json:"heap_internal_largest_bytes"`
	PSRAMFreeBytes        *int64    `json:"psram_free_bytes"`
	PSRAMMinBytes         *int64    `json:"psram_min_bytes"`
	PSRAMLargestBytes     *int64    `json:"psram_largest_bytes"`
	TempC                 *float64  `json:"temp_c"`
	RequestsPerMin        *float64  `json:"requests_per_min"`
	RequestFailuresPerMin *float64  `json:"request_failures_per_min"`
	RequestLatencyAvgMS   *float64  `json:"request_latency_avg_ms"`
	RequestLatencyMaxMS   *int64    `json:"request_latency_max_ms"`
	RenderFPS             *float64  `json:"render_fps"`
	FrameAvgMS            *float64  `json:"frame_avg_ms"`
	FrameMaxMS            *int64    `json:"frame_max_ms"`

	periodMS    int64
	resetReason string
}

func refOf[T any](v T) *T { return &v }

// knobSampleFromReport builds a sample from a checkin's top-level fields
// (RSSI, internal heap, uptime) and its stats object.
func knobSampleFromReport(c deviceCheckin, s *knobStatsReport) knobSample {
	out := knobSample{T: c.SeenAt, periodMS: s.PeriodMS, resetReason: s.ResetReason}
	if out.periodMS == 0 {
		out.periodMS = 60_000
	}
	if c.RSSI != 0 {
		out.RSSIDBm = refOf(c.RSSI)
	}
	if c.HeapInternalFree > 0 {
		out.HeapInternalFreeBytes = refOf(int64(c.HeapInternalFree))
	}
	if c.HeapInternalLargest > 0 {
		out.HeapInternalLargest = refOf(int64(c.HeapInternalLargest))
	}
	if c.UptimeS > 0 {
		out.UptimeSec = refOf(c.UptimeS)
	}
	if len(s.CPUPct) > 0 {
		out.CPUPercent = append([]float64(nil), s.CPUPct...)
	}
	out.HeapInternalMinBytes = s.HeapInternalMin
	out.PSRAMFreeBytes, out.PSRAMMinBytes, out.PSRAMLargestBytes = s.PSRAMFree, s.PSRAMMin, s.PSRAMLargest
	out.TempC = s.TempC
	perMin := func(n int64) *float64 { return refOf(float64(n) * 60_000 / float64(out.periodMS)) }
	if s.ReqOK != nil || s.ReqFail != nil {
		var ok, fail int64
		if s.ReqOK != nil {
			ok = *s.ReqOK
		}
		if s.ReqFail != nil {
			fail = *s.ReqFail
		}
		out.RequestsPerMin, out.RequestFailuresPerMin = perMin(ok+fail), perMin(fail)
	}
	out.RequestLatencyAvgMS, out.RequestLatencyMaxMS = s.ReqMSAvg, s.ReqMSMax
	out.RenderFPS, out.FrameAvgMS, out.FrameMaxMS = s.FPS, s.FrameMSAvg, s.FrameMSMax
	return out
}

// merge folds b (newer) into a: gauges take b, low-water marks the min,
// maxima the max, averages and rates the period-weighted mean.
func (a knobSample) merge(b knobSample) knobSample {
	wa, wb := float64(a.periodMS), float64(b.periodMS)
	out := b
	out.periodMS = a.periodMS + b.periodMS
	if b.resetReason == "" {
		out.resetReason = a.resetReason
	}
	mean := func(x, y *float64) *float64 {
		switch {
		case x == nil:
			return y
		case y == nil:
			return x
		}
		return refOf((*x*wa + *y*wb) / (wa + wb))
	}
	lowest := func(x, y *int64) *int64 {
		if x == nil || (y != nil && *y < *x) {
			return y
		}
		return x
	}
	highest := func(x, y *int64) *int64 {
		if x == nil || (y != nil && *y > *x) {
			return y
		}
		return x
	}
	latest := func(x, y *int64) *int64 {
		if y == nil {
			return x
		}
		return y
	}
	if len(a.CPUPercent) == len(b.CPUPercent) {
		out.CPUPercent = make([]float64, len(b.CPUPercent))
		for i := range b.CPUPercent {
			out.CPUPercent[i] = (a.CPUPercent[i]*wa + b.CPUPercent[i]*wb) / (wa + wb)
		}
	} else if len(b.CPUPercent) == 0 {
		out.CPUPercent = a.CPUPercent
	}
	if b.RSSIDBm == nil {
		out.RSSIDBm = a.RSSIDBm
	}
	if b.TempC == nil {
		out.TempC = a.TempC
	}
	out.UptimeSec = latest(a.UptimeSec, b.UptimeSec)
	out.HeapInternalFreeBytes = latest(a.HeapInternalFreeBytes, b.HeapInternalFreeBytes)
	out.HeapInternalLargest = latest(a.HeapInternalLargest, b.HeapInternalLargest)
	out.PSRAMFreeBytes = latest(a.PSRAMFreeBytes, b.PSRAMFreeBytes)
	out.PSRAMLargestBytes = latest(a.PSRAMLargestBytes, b.PSRAMLargestBytes)
	out.HeapInternalMinBytes = lowest(a.HeapInternalMinBytes, b.HeapInternalMinBytes)
	out.PSRAMMinBytes = lowest(a.PSRAMMinBytes, b.PSRAMMinBytes)
	out.RequestsPerMin = mean(a.RequestsPerMin, b.RequestsPerMin)
	out.RequestFailuresPerMin = mean(a.RequestFailuresPerMin, b.RequestFailuresPerMin)
	out.RequestLatencyAvgMS = mean(a.RequestLatencyAvgMS, b.RequestLatencyAvgMS)
	out.RequestLatencyMaxMS = highest(a.RequestLatencyMaxMS, b.RequestLatencyMaxMS)
	out.RenderFPS = mean(a.RenderFPS, b.RenderFPS)
	out.FrameAvgMS = mean(a.FrameAvgMS, b.FrameAvgMS)
	out.FrameMaxMS = highest(a.FrameMaxMS, b.FrameMaxMS)
	return out
}

func (s knobSample) stamp() time.Time { return s.T }

type knobSeries struct {
	sampleSeries[knobSample]
	liveUntil time.Time
}

// knobStatsStore keeps each knob's diagnostics samples and live-mode
// deadline in memory only: a restart loses them, and a checkin never writes
// the store for them.
type knobStatsStore struct {
	now func() time.Time

	mu     sync.Mutex // protects series
	series map[string]*knobSeries
}

func newKnobStatsStore() *knobStatsStore {
	return &knobStatsStore{now: time.Now, series: map[string]*knobSeries{}}
}

func (k *knobStatsStore) seriesLocked(id string) *knobSeries {
	s := k.series[id]
	if s == nil {
		s = &knobSeries{sampleSeries: newSampleSeries[knobSample](knobStatsLiveCap)}
		k.series[id] = s
	}
	return s
}

// record adds a sample at now: whole to the live ring, folded into the
// current minute's bucket in the minute ring.
func (k *knobStatsStore) record(id string, now time.Time, s knobSample) {
	s.T = now.UTC().Truncate(time.Second)
	k.mu.Lock()
	defer k.mu.Unlock()
	k.seriesLocked(id).record(s)
}

// setLive sets id's live-mode deadline; a zero time stops it.
func (k *knobStatsStore) setLive(id string, until time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.seriesLocked(id).liveUntil = until
}

// liveUntil is id's live-mode deadline, nil when not live at now.
func (k *knobStatsStore) liveUntil(id string, now time.Time) *time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	s := k.series[id]
	if s == nil || !s.liveUntil.After(now) {
		return nil
	}
	t := s.liveUntil.UTC().Truncate(time.Second)
	return &t
}

func (k *knobStatsStore) forget(id string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.series, id)
}

func (k *knobStatsStore) minuteLen(id string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if s := k.series[id]; s != nil {
		return s.minutes.len
	}
	return 0
}

// points returns id's samples in rng's window ending at now, oldest
// first, and the newest sample.
func (k *knobStatsStore) points(id, rng string, now time.Time) ([]knobSample, *knobSample) {
	k.mu.Lock()
	defer k.mu.Unlock()
	s := k.series[id]
	if s == nil {
		return []knobSample{}, nil
	}
	var latest *knobSample
	if l := s.live.last(); l != nil {
		c := *l
		latest = &c
	}
	return s.points(rng, now), latest
}

// knobStatsView is GET /v1/devices/{id}/stats.
type knobStatsView struct {
	DeviceID    string       `json:"device_id"`
	Diagnostics string       `json:"diagnostics"`
	Range       string       `json:"range"`
	Online      bool         `json:"online"`
	LastSeen    *time.Time   `json:"last_seen"`
	LiveUntil   *time.Time   `json:"live_until"`
	ResetReason *string      `json:"reset_reason"`
	Latest      *knobSample  `json:"latest"`
	Points      []knobSample `json:"points"`
}

func (a *App) handleKnobStats(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "1h"
	}
	if _, ok := statsRanges[rng]; !ok {
		a.writeDeviceError(w, r, fmt.Errorf("%w: range must be 15m, 1h or 24h", errDeviceBody))
		return
	}
	v, err := a.buildKnobStats(r.PathValue("id"), rng, a.knobStats.now())
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, v)
}

// buildKnobStats is device id's stats over rng (a statsRanges key) at now.
func (a *App) buildKnobStats(id, rng string, now time.Time) (knobStatsView, error) {
	diag, last, err := a.devices.diagnostics(id)
	if err != nil {
		return knobStatsView{}, err
	}
	points, latest := a.knobStats.points(id, rng, now)
	v := knobStatsView{
		DeviceID:    id,
		Diagnostics: diag,
		Range:       rng,
		Points:      points,
		Latest:      latest,
	}
	if diag != knobDiagOff {
		v.LiveUntil = a.knobStats.liveUntil(id, now)
	}
	if last != nil {
		t := last.SeenAt.UTC().Truncate(time.Second)
		v.LastSeen = &t
		v.Online = now.Sub(last.SeenAt) <= knobOnlineWindow
	}
	if latest != nil && latest.resetReason != "" {
		v.ResetReason = refOf(latest.resetReason)
	}
	return v, nil
}

var errDiagnosticsOff = errors.New("diagnostics are off for this device")

func (a *App) handleKnobStatsLive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds *int `json:"seconds"`
	}
	if !a.decodeOptionalOrReject(w, r, &req, true) {
		return
	}
	d := knobLiveDefault
	if req.Seconds != nil {
		d = time.Duration(*req.Seconds) * time.Second
		if d < 0 || d > knobLiveMax {
			a.writeDeviceError(w, r, fmt.Errorf("%w: seconds must be 0..%d", errDeviceBody, int(knobLiveMax.Seconds())))
			return
		}
	}
	id := r.PathValue("id")
	diag, _, err := a.devices.diagnostics(id)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if diag == knobDiagOff {
		writeError(w, http.StatusConflict, errDiagnosticsOff)
		return
	}
	now := a.knobStats.now()
	var until time.Time
	if d > 0 {
		until = now.Add(d)
	}
	a.knobStats.setLive(id, until)
	writeJSON(w, http.StatusOK, map[string]*time.Time{"live_until": a.knobStats.liveUntil(id, now)})
}

// knobLiveUnix is id's live-mode deadline in Unix seconds for the knob, nil
// when not live or diagnostics are off.
func (a *App) knobLiveUnix(id, diag string, now time.Time) *int64 {
	if diag == knobDiagOff {
		return nil
	}
	if t := a.knobStats.liveUntil(id, now); t != nil {
		return refOf(t.Unix())
	}
	return nil
}

// decodeKnobStats parses a checkin's raw stats object; nil when absent or
// invalid (logged), so a firmware bug never costs the checkin itself.
func (a *App) decodeKnobStats(r *http.Request, raw json.RawMessage) *knobStatsReport {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s knobStatsReport
	err := json.Unmarshal(raw, &s)
	if err == nil {
		err = s.validate()
	}
	if err != nil {
		a.logger.InfoContext(r.Context(), "device stats dropped", "device_id", deviceIDFrom(r.Context()), "err", err)
		return nil
	}
	return &s
}

func unixHeader(now time.Time) string { return strconv.FormatInt(now.Unix(), 10) }
