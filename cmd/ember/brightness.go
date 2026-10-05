package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Display brightness policy. GET /v1/display/brightness answers one
// 0-255 level for devices with no light sensor of their own (the cinder knob),
// so they need no Home Assistant token. Two inputs, in order:
//
//  1. the TC001's light sensor, read through the shared clock probe (the
//     device watch or clock stats sampler keeps it fresh), smoothed with an EMA and
//     held steady inside a hysteresis band;
//  2. when the clock is unreachable or its reading is stale, the sun schedule
//     from the weather lat/lon with a twilight ramp.
//
// The policy below is pure (config + state + sample + time in, level out) so
// the table tests own every branch. A server ticker feeds the filter
// (tickBrightness); requests only read it (brightnessAt), so the answer does
// not depend on how many clients poll. This never touches the clock's own
// brightness.

const brightnessSettingsKey = "brightness_json"

const minStaleSeconds = int(2 * clockProbeTTL / time.Second)

// BrightnessConfig is the brightness policy. Zero fields take the defaults in
// resolved(), so every field's valid range excludes 0 and a stored 0 can never
// disagree with validate. Hysteresis is the smallest level change that moves a
// held level.
type BrightnessConfig struct {
	Floor           int     `json:"floor"`
	Ceiling         int     `json:"ceiling"`
	NightLevel      int     `json:"night_level"`
	DayLevel        int     `json:"day_level"`
	LuxDark         float64 `json:"lux_dark"`
	LuxBright       float64 `json:"lux_bright"`
	EMAAlpha        float64 `json:"ema_alpha"`
	Hysteresis      int     `json:"hysteresis"`
	StaleSeconds    int     `json:"stale_seconds"`
	TwilightMinutes int     `json:"twilight_minutes"`
}

func (c BrightnessConfig) resolved() BrightnessConfig {
	if c.Floor == 0 {
		c.Floor = 10
	}
	if c.Ceiling == 0 {
		c.Ceiling = 255
	}
	if c.NightLevel == 0 {
		c.NightLevel = 20
	}
	if c.DayLevel == 0 {
		c.DayLevel = 255
	}
	if c.LuxDark == 0 {
		c.LuxDark = 1
	}
	if c.LuxBright == 0 {
		c.LuxBright = 200
	}
	if c.EMAAlpha == 0 {
		c.EMAAlpha = 0.3
	}
	if c.Hysteresis == 0 {
		c.Hysteresis = 8
	}
	if c.StaleSeconds == 0 {
		c.StaleSeconds = 120
	}
	if c.TwilightMinutes == 0 {
		c.TwilightMinutes = 45
	}
	return c
}

func (c BrightnessConfig) validate() error {
	switch {
	case c.Floor < 1 || c.Floor > 255:
		return fmt.Errorf("floor %d out of range [1, 255]", c.Floor)
	case c.Ceiling < c.Floor || c.Ceiling > 255:
		return fmt.Errorf("ceiling %d out of range [floor, 255]", c.Ceiling)
	case c.NightLevel < c.Floor || c.NightLevel > c.Ceiling:
		return fmt.Errorf("night_level %d out of range [floor, ceiling]", c.NightLevel)
	case c.DayLevel < c.Floor || c.DayLevel > c.Ceiling:
		return fmt.Errorf("day_level %d out of range [floor, ceiling]", c.DayLevel)
	case c.LuxDark <= 0 || c.LuxBright <= c.LuxDark:
		return fmt.Errorf("lux_dark %v and lux_bright %v must satisfy 0 < dark < bright", c.LuxDark, c.LuxBright)
	case c.EMAAlpha <= 0 || c.EMAAlpha > 1:
		return fmt.Errorf("ema_alpha %v out of range (0, 1]", c.EMAAlpha)
	case c.Hysteresis < 1 || c.Hysteresis > 50:
		return fmt.Errorf("hysteresis %d out of range [1, 50]", c.Hysteresis)
	case c.StaleSeconds < minStaleSeconds || c.StaleSeconds > 3600:
		return fmt.Errorf("stale_seconds %d out of range [%d, 3600]", c.StaleSeconds, minStaleSeconds)
	case c.TwilightMinutes < 1 || c.TwilightMinutes > 180:
		return fmt.Errorf("twilight_minutes %d out of range [1, 180]", c.TwilightMinutes)
	}
	return nil
}

// brightnessSettingSpec registers the policy with the settings overlay, so
// GET/PUT /v1/brightness/config merges like every other settings PUT.
func brightnessSettingSpec() settingSpec[BrightnessConfig] {
	return settingSpec[BrightnessConfig]{
		key:  brightnessSettingsKey,
		view: func(c Config) BrightnessConfig { return c.Brightness.resolved() },
		apply: func(c *Config, d BrightnessConfig) error {
			if err := d.validate(); err != nil {
				return err
			}
			c.Brightness = d
			return nil
		},
	}
}

// luxSample is one clock reading. At is when the clock was probed, so a
// cached probe handed out twice is recognisably the same sample.
type luxSample struct {
	Lux float64
	At  time.Time
}

// brightnessGeo is the weather location; Set is false while unconfigured.
type brightnessGeo struct {
	Lat, Lon float64
	Set      bool
}

// brightnessState is what survives between requests. Last is the newest clock
// sample seen, kept across failed probes until it goes stale; SampleAt is the
// newest sample already fed to the EMA.
type brightnessState struct {
	EMA      float64
	HasEMA   bool
	Level    int
	HasLevel bool
	SampleAt time.Time
	Last     luxSample
	HasLast  bool
}

// brightnessOut is the GET /v1/display/brightness body.
type brightnessOut struct {
	Level  int    `json:"level"`
	Source string `json:"source"`
	Night  bool   `json:"night"`
}

// luxToLevel maps lux onto [Floor, Ceiling] on a log scale between LuxDark and
// LuxBright: perceived brightness is roughly logarithmic in lux.
func luxToLevel(c BrightnessConfig, lux float64) int {
	switch {
	case lux <= c.LuxDark:
		return c.Floor
	case lux >= c.LuxBright:
		return c.Ceiling
	}
	f := math.Log(lux/c.LuxDark) / math.Log(c.LuxBright/c.LuxDark)
	return c.Floor + int(math.Round(f*float64(c.Ceiling-c.Floor)))
}

// holdWithinBand keeps prev unless target is at least band away. The extremes
// always snap, so the screen can reach the floor and ceiling exactly.
func holdWithinBand(prev int, hasPrev bool, target, band, floor, ceiling int) int {
	if !hasPrev || target == floor || target == ceiling {
		return target
	}
	prev = min(max(prev, floor), ceiling)
	d := target - prev
	if d < 0 {
		d = -d
	}
	if d < band {
		return prev
	}
	return target
}

// sunLevel is the fallback schedule: DayLevel from sunrise to sunset, then a
// linear ramp down to NightLevel over TwilightMinutes, and the mirror ramp up
// ending at sunrise. night is true between sunset and sunrise. Events from the
// neighbouring UTC dates are included because sunTimes works per UTC date and a
// western evening falls on the next one. Without a sunrise or sunset (polar
// day or night) it follows the sun's altitude at noon.
func sunLevel(c BrightnessConfig, lat, lon float64, now time.Time) (level int, night bool) {
	type event struct {
		at   time.Time
		rise bool
	}
	var evs []event
	for d := -1; d <= 1; d++ {
		if rise, set, ok := sunTimes(lat, lon, now.AddDate(0, 0, d)); ok {
			evs = append(evs, event{rise, true}, event{set, false})
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].at.Before(evs[j].at) })

	var last, next *event
	for i := range evs {
		if !evs[i].at.After(now) {
			last = &evs[i]
		} else if next == nil {
			next = &evs[i]
		}
	}
	if last == nil || next == nil {
		if polarNight(lat, now) {
			return c.NightLevel, true
		}
		return c.DayLevel, false
	}
	night = !last.rise
	tw := time.Duration(c.TwilightMinutes) * time.Minute
	frac := 1.0
	if night {
		frac = 0
		if since := now.Sub(last.at); since < tw {
			frac = 1 - float64(since)/float64(tw)
		} else if until := next.at.Sub(now); next.rise && until < tw {
			frac = 1 - float64(until)/float64(tw)
		}
	}
	return c.NightLevel + int(math.Round(frac*float64(c.DayLevel-c.NightLevel))), night
}

// polarNight reports whether the sun stays below the horizon at noon on now's
// date at lat, using the solar declination for the day of the year.
func polarNight(lat float64, now time.Time) bool {
	decl := -23.44 * math.Cos(2*math.Pi*float64(now.UTC().YearDay()+10)/365)
	noonAltitude := 90 - math.Abs(lat-decl)
	return noonAltitude < -0.833
}

// decideBrightness is the whole policy. The newest clock sample is remembered
// across failed probes and used until it is StaleSeconds old, then the filter
// resets and the sun schedule answers, then DayLevel when no location is set.
// Each sample feeds the EMA once and only if newer than the last; a gap longer
// than StaleSeconds reseeds it.
func decideBrightness(c BrightnessConfig, st brightnessState, s *luxSample, geo brightnessGeo, now time.Time) (brightnessOut, brightnessState) {
	night := false
	var sunNow int
	if geo.Set {
		sunNow, night = sunLevel(c, geo.Lat, geo.Lon, now)
	}
	stale := time.Duration(c.StaleSeconds) * time.Second
	if s != nil && (!st.HasLast || s.At.After(st.Last.At)) {
		st.Last, st.HasLast = *s, true
	}
	if st.HasLast && now.Sub(st.Last.At) <= stale {
		if st.Last.At.After(st.SampleAt) {
			if st.HasEMA && st.Last.At.Sub(st.SampleAt) <= stale {
				st.EMA = c.EMAAlpha*st.Last.Lux + (1-c.EMAAlpha)*st.EMA
			} else {
				st.EMA, st.HasEMA = st.Last.Lux, true
			}
			st.SampleAt = st.Last.At
		}
		st.Level = holdWithinBand(st.Level, st.HasLevel, luxToLevel(c, st.EMA), c.Hysteresis, c.Floor, c.Ceiling)
		st.HasLevel = true
		return brightnessOut{Level: st.Level, Source: "lux", Night: night}, st
	}
	st.HasEMA, st.HasLevel = false, false
	if geo.Set {
		return brightnessOut{Level: sunNow, Source: "sun", Night: night}, st
	}
	return brightnessOut{Level: c.DayLevel, Source: "default"}, st
}

// brightnessAt answers the policy at now from the filter state without
// changing it: the held lux level while the newest sample is fresh (re-held
// against the current config), else the sun schedule, else DayLevel. A read
// allows one tick plus one probe of extra age, since the filter only moves
// on the tick.
func brightnessAt(c BrightnessConfig, st brightnessState, geo brightnessGeo, now time.Time) brightnessOut {
	night := false
	var sunNow int
	if geo.Set {
		sunNow, night = sunLevel(c, geo.Lat, geo.Lon, now)
	}
	fresh := time.Duration(c.StaleSeconds)*time.Second + brightnessTickInterval + clockProbeTimeout
	if st.HasLevel && st.HasLast && now.Sub(st.Last.At) <= fresh {
		level := holdWithinBand(st.Level, true, luxToLevel(c, st.EMA), c.Hysteresis, c.Floor, c.Ceiling)
		return brightnessOut{Level: level, Source: "lux", Night: night}
	}
	if geo.Set {
		return brightnessOut{Level: sunNow, Source: "sun", Night: night}
	}
	return brightnessOut{Level: c.DayLevel, Source: "default"}
}

// brightnessTickInterval is how often the server feeds the clock's light
// reading to the filter; reads never advance it.
const brightnessTickInterval = time.Minute

// brightnessTracker holds the filter state between ticks.
type brightnessTracker struct {
	mu sync.Mutex // guards st
	st brightnessState
}

func (a *App) brightnessGeo() brightnessGeo {
	w := a.cfg.Load().Weather
	return brightnessGeo{Lat: w.Latitude, Lon: w.Longitude, Set: w.Latitude != 0 || w.Longitude != 0}
}

// tickBrightness reads the clock's cached probe and advances the filter once.
func (a *App) tickBrightness(ctx context.Context, now time.Time) {
	var sample *luxSample
	if dev := a.probeClockHealth(ctx, now); dev != nil && dev.Reachable && dev.lightLevel != nil {
		sample = &luxSample{Lux: *dev.lightLevel, At: dev.CheckedAt}
	}
	c := a.cfg.Load().Brightness.resolved()
	geo := a.brightnessGeo()
	a.brightness.mu.Lock()
	prev := a.brightness.st
	_, a.brightness.st = decideBrightness(c, a.brightness.st, sample, geo, now)
	moved := a.brightness.st.Level != prev.Level || a.brightness.st.HasLevel != prev.HasLevel
	a.brightness.mu.Unlock()
	if moved {
		a.changes.notify(topicBrightness)
	}
}

// StartBrightness ticks the filter at once and then every
// brightnessTickInterval until ctx is done.
func (a *App) StartBrightness(ctx context.Context) {
	a.tickBrightness(ctx, time.Now())
	t := time.NewTicker(brightnessTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.tickBrightness(ctx, now)
		}
	}
}

// currentBrightness is the read-only answer for now.
func (a *App) currentBrightness(now time.Time) brightnessOut {
	c := a.cfg.Load().Brightness.resolved()
	geo := a.brightnessGeo()
	a.brightness.mu.Lock()
	st := a.brightness.st
	a.brightness.mu.Unlock()
	return brightnessAt(c, st, geo, now)
}

func (a *App) handleDisplayBrightness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.currentBrightness(time.Now()))
}

func (a *App) handleBrightnessConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.brightness)
}

func (a *App) handleBrightnessConfigPut(w http.ResponseWriter, r *http.Request) {
	if d, ok := serveSettingPut(a, w, r, a.settings.brightness); ok {
		writeJSON(w, http.StatusOK, d)
	}
}
