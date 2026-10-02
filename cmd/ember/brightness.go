package main

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Display brightness policy (#211). GET /v1/display/brightness answers one
// 0-255 level for devices with no light sensor of their own (the cinder knob),
// so they need no Home Assistant token. Two inputs, in order:
//
//  1. the TC001's light sensor, read through the clock probe the health
//     endpoint already keeps fresh (no new poller), smoothed with an EMA and
//     held steady inside a hysteresis band;
//  2. when the clock is unreachable or its reading is stale, the sun schedule
//     from the weather lat/lon with a twilight ramp.
//
// The policy below is pure (config + state + sample + time in, level out) so
// the table tests own every branch. This never touches the clock's own
// brightness.

const brightnessSettingsKey = "brightness_json"

// BrightnessConfig is the brightness policy. In config.json zero fields take
// the defaults in resolved(); the settings overlay always works on the
// resolved form.
type BrightnessConfig struct {
	Floor           int     `json:"floor"`            // lowest level the lux path emits
	Ceiling         int     `json:"ceiling"`          // highest level the lux path emits
	NightLevel      int     `json:"night_level"`      // sun fallback, after dusk
	DayLevel        int     `json:"day_level"`        // sun fallback, by day
	LuxDark         float64 `json:"lux_dark"`         // at or below: floor
	LuxBright       float64 `json:"lux_bright"`       // at or above: ceiling
	EMAAlpha        float64 `json:"ema_alpha"`        // weight of the newest sample, (0,1]
	Hysteresis      int     `json:"hysteresis"`       // level units a change must exceed
	StaleSeconds    int     `json:"stale_seconds"`    // older clock readings are ignored
	TwilightMinutes int     `json:"twilight_minutes"` // ramp length at dawn and dusk
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
		c.LuxDark = 5
	}
	if c.LuxBright == 0 {
		c.LuxBright = 300
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
	case c.Hysteresis < 0 || c.Hysteresis > 50:
		return fmt.Errorf("hysteresis %d out of range [0, 50]", c.Hysteresis)
	case c.StaleSeconds < 10 || c.StaleSeconds > 3600:
		return fmt.Errorf("stale_seconds %d out of range [10, 3600]", c.StaleSeconds)
	case c.TwilightMinutes < 0 || c.TwilightMinutes > 180:
		return fmt.Errorf("twilight_minutes %d out of range [0, 180]", c.TwilightMinutes)
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

// brightnessState is what survives between requests.
type brightnessState struct {
	EMA      float64
	HasEMA   bool
	Level    int
	HasLevel bool
	SampleAt time.Time
}

// brightnessOut is the GET /v1/display/brightness body.
type brightnessOut struct {
	Level  int    `json:"level"`
	Source string `json:"source"` // "lux", "sun" or "default"
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
// ending at sunrise. night is true between sunset and sunrise (the same call
// as isNight). Events from the neighbouring UTC dates are included because
// sunTimes works per UTC date and a western evening falls on the next one.
// Polar day or night (no events) reads as day.
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
		return c.DayLevel, false
	}
	night = !last.rise
	tw := time.Duration(c.TwilightMinutes) * time.Minute
	frac := 1.0 // 0 = night level, 1 = day level
	if night {
		frac = 0
		if since := now.Sub(last.at); since < tw {
			frac = 1 - float64(since)/float64(tw) // dusk ramp
		} else if until := next.at.Sub(now); next.rise && until < tw {
			frac = 1 - float64(until)/float64(tw) // dawn ramp
		}
	}
	return c.NightLevel + int(math.Round(frac*float64(c.DayLevel-c.NightLevel))), night
}

// decideBrightness is the whole policy. A fresh clock reading feeds the EMA
// once per distinct sample; a missing or stale one resets the filter and falls
// back to the sun schedule, then to DayLevel when no location is set.
func decideBrightness(c BrightnessConfig, st brightnessState, s *luxSample, geo brightnessGeo, now time.Time) (brightnessOut, brightnessState) {
	night := false
	var sunNow int
	if geo.Set {
		sunNow, night = sunLevel(c, geo.Lat, geo.Lon, now)
	}
	if s != nil && now.Sub(s.At) <= time.Duration(c.StaleSeconds)*time.Second {
		if !s.At.Equal(st.SampleAt) {
			if st.HasEMA {
				st.EMA = c.EMAAlpha*s.Lux + (1-c.EMAAlpha)*st.EMA
			} else {
				st.EMA, st.HasEMA = s.Lux, true
			}
			st.SampleAt = s.At
		}
		st.Level = holdWithinBand(st.Level, st.HasLevel, luxToLevel(c, st.EMA), c.Hysteresis, c.Floor, c.Ceiling)
		st.HasLevel = true
		return brightnessOut{Level: st.Level, Source: "lux", Night: night}, st
	}
	st = brightnessState{}
	if geo.Set {
		return brightnessOut{Level: sunNow, Source: "sun", Night: night}, st
	}
	return brightnessOut{Level: c.DayLevel, Source: "default"}, st
}

// brightnessTracker holds the filter state between requests.
type brightnessTracker struct {
	mu sync.Mutex // guards st
	st brightnessState
}

// currentBrightness reads the clock's cached probe and applies the policy.
func (a *App) currentBrightness(r *http.Request, now time.Time) brightnessOut {
	cfg := a.cfg.Load()
	var sample *luxSample
	if dev := a.probeClockHealth(r.Context(), now); dev != nil && dev.Reachable && dev.lightLevel != nil {
		sample = &luxSample{Lux: *dev.lightLevel, At: dev.CheckedAt}
	}
	geo := brightnessGeo{
		Lat: cfg.Weather.Latitude, Lon: cfg.Weather.Longitude,
		Set: cfg.Weather.Latitude != 0 || cfg.Weather.Longitude != 0,
	}
	a.brightness.mu.Lock()
	defer a.brightness.mu.Unlock()
	out, st := decideBrightness(cfg.Brightness.resolved(), a.brightness.st, sample, geo, now)
	a.brightness.st = st
	return out
}

func (a *App) handleDisplayBrightness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.currentBrightness(r, time.Now()))
}

func (a *App) handleBrightnessConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.brightness)
}

func (a *App) handleBrightnessConfigPut(w http.ResponseWriter, r *http.Request) {
	if d, ok := serveSettingPut(a, w, r, a.settings.brightness); ok {
		writeJSON(w, http.StatusOK, d)
	}
}
