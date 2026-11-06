package weather

import (
	"cmp"
	"sort"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

type Config struct {
	Enabled              bool              `json:"enabled"`
	Provider             string            `json:"provider"`
	OpenMeteoModel       string            `json:"open_meteo_model"`
	Latitude             float64           `json:"latitude"`
	Longitude            float64           `json:"longitude"`
	LocationName         string            `json:"location_name"`
	Units                string            `json:"units"`
	RefreshMinutes       int               `json:"refresh_minutes"`
	RotateInApps         *bool             `json:"rotate_in_apps"`
	ForecastTile         *bool             `json:"forecast_tile"`
	ForecastHours        int               `json:"forecast_hours"`
	SunPopups            *bool             `json:"sun_popups"`
	MoonPhase            *bool             `json:"moon_phase"`
	PopupIntervalMinutes *int              `json:"popup_interval_minutes"`
	PopupDurationSeconds int               `json:"popup_duration_seconds"`
	PopupOnChange        *bool             `json:"popup_on_change"`
	SevereAlert          *bool             `json:"severe_alert"`
	SevereSound          string            `json:"severe_sound"`
	UseNativeIcons       bool              `json:"use_native_icons"`
	IconIDs              map[string]string `json:"icon_ids,omitempty"`
	TileNativeIcons      bool              `json:"tile_native_icons"`
	AirTile              *bool             `json:"air_tile"`
	AirPopupThreshold    int               `json:"air_popup_threshold"`
	Overlay              *bool             `json:"overlay"`
}

const defaultWeatherPopupIntervalMinutes = 120

func (c Config) RotateInAppsEnabled() bool { return c.RotateInApps == nil || *c.RotateInApps }

func (c Config) ForecastTileEnabled() bool { return c.ForecastTile == nil || *c.ForecastTile }

func (c Config) SunPopupsEnabled() bool { return c.SunPopups == nil || *c.SunPopups }

func (c Config) MoonPhaseEnabled() bool { return c.MoonPhase == nil || *c.MoonPhase }

func (c Config) PopupOnChangeEnabled() bool { return c.PopupOnChange == nil || *c.PopupOnChange }

func (c Config) SevereAlertEnabled() bool { return c.SevereAlert == nil || *c.SevereAlert }

func (c Config) AirTileEnabled() bool { return c.AirTile == nil || *c.AirTile }

func (c Config) OverlayEnabled() bool { return c.Overlay == nil || *c.Overlay }

// nil means 120, explicit 0 turns it off.
func (c Config) PopupIntervalMins() int {
	if c.PopupIntervalMinutes == nil {
		return defaultWeatherPopupIntervalMinutes
	}
	if v := *c.PopupIntervalMinutes; v > 0 {
		return v
	}
	return 0
}

func (c *Config) FillAbsent() {
	if c.OpenMeteoModel == "" {
		c.OpenMeteoModel = DefaultOpenMeteoModel
	}
	if c.RotateInApps == nil {
		c.RotateInApps = boolPtr(true)
	}
	if c.ForecastTile == nil {
		c.ForecastTile = boolPtr(true)
	}
	if c.SunPopups == nil {
		c.SunPopups = boolPtr(true)
	}
	if c.MoonPhase == nil {
		c.MoonPhase = boolPtr(true)
	}
	if c.PopupOnChange == nil {
		c.PopupOnChange = boolPtr(true)
	}
	if c.SevereAlert == nil {
		c.SevereAlert = boolPtr(true)
	}
	if c.AirTile == nil {
		c.AirTile = boolPtr(true)
	}
	if c.Overlay == nil {
		c.Overlay = boolPtr(true)
	}
	if c.PopupIntervalMinutes == nil {
		c.PopupIntervalMinutes = intPtr(defaultWeatherPopupIntervalMinutes)
	}
}

func (c Config) IconID(cond string) string {
	if id := c.IconIDs[cond]; id != "" {
		return id
	}
	return defaultWeatherIconIDs[cond]
}

var defaultWeatherIconIDs = map[string]string{
	render.WeatherClear:  "1338",
	render.WeatherClouds: "2286",
	render.WeatherFog:    "17056",
	render.WeatherRain:   "72",
	render.WeatherSnow:   "2289",
	render.WeatherStorm:  "11428",
}

func (c *Config) ApplyDefaults() {
	if c.Provider == "" {
		c.Provider = "open-meteo"
	}
	if c.Units == "" {
		c.Units = "metric"
	}
	if c.RefreshMinutes <= 0 {
		c.RefreshMinutes = 10
	}
	if c.PopupDurationSeconds <= 0 {
		c.PopupDurationSeconds = 30
	}
	c.FillAbsent()
	if *c.PopupIntervalMinutes < 0 {
		c.PopupIntervalMinutes = intPtr(0)
	}
	if c.ForecastHours <= 0 || c.ForecastHours > 24 {
		c.ForecastHours = 24
	}
	if c.AirPopupThreshold < 0 {
		c.AirPopupThreshold = 0
	} else if c.AirPopupThreshold == 0 {
		c.AirPopupThreshold = 80
	}
}

type Observation struct {
	Condition       string
	ConditionCode   string
	TempC           float64
	Severe          bool
	ObservedAt      time.Time
	FetchedAt       time.Time
	HourlyStart     time.Time
	Hourly          []float64
	Overlay         string
	TZOffsetSeconds int
	TZKnown         bool
	ValidUntil      time.Time
	Now             Now
	PrecipStep      time.Duration
	Precip          []PrecipStep
	Days            []Day
	Steps           []currentStep
}

type currentStep struct {
	At        time.Time
	Condition string
	Code      string
	TempC     float64
	Severe    bool
	Overlay   string
	Now       Now
}

func (o Observation) Current(now time.Time) Observation {
	i := sort.Search(len(o.Steps), func(i int) bool { return o.Steps[i].At.After(now) }) - 1
	if i < 0 {
		return o
	}
	c := o.Steps[i]
	o.Condition, o.ConditionCode, o.TempC, o.Severe = c.Condition, c.Code, c.TempC, c.Severe
	o.Overlay, o.Now, o.ObservedAt = c.Overlay, c.Now, c.At
	return o
}

type Now struct {
	Cloud   *float64
	UV      *float64
	WindKmh *float64
	GustKmh *float64
	WindDir *float64
}

type PrecipStep struct {
	At   time.Time
	MMh  float64
	Snow bool
}

type Day struct {
	Start      time.Time
	End        time.Time
	Cond       string
	Code       string
	MinC       *float64
	MaxC       *float64
	PrecipMM   *float64
	Pop        *float64
	UVMax      *float64
	GustMaxKmh *float64
}

const forecastFetchHours = 24

const airFetchHours = 24

const forecastDays = 4

type AirObservation struct {
	AQI             float64
	PM25            float64
	PM10            float64
	HourlyAQI       []float64
	HourlyStart     time.Time
	FetchedAt       time.Time
	TZOffsetSeconds int
	TZKnown         bool
	TZName          string
}

type Location struct{ Lat, Lon float64 }

type Source struct {
	Provider string
	Model    string
	Loc      Location
}

func (c Config) Location() Location { return Location{c.Latitude, c.Longitude} }

func (c Config) Source() Source {
	return Source{c.Provider, c.Model(), c.Location()}
}

func (c Config) Model() string {
	if c.Provider != "open-meteo" {
		return ""
	}
	return cmp.Or(c.OpenMeteoModel, DefaultOpenMeteoModel)
}

func wmoCondition(code int) (string, bool) {
	switch {
	case code == 0:
		return render.WeatherClear, false
	case code >= 1 && code <= 3:
		return render.WeatherClouds, false
	case code == 45 || code == 48:
		return render.WeatherFog, false
	case code >= 51 && code <= 57:
		return render.WeatherRain, false
	case code >= 61 && code <= 67:
		return render.WeatherRain, code == 65 || code == 67
	case code >= 71 && code <= 77:
		return render.WeatherSnow, code == 75
	case code >= 80 && code <= 82:
		return render.WeatherRain, code == 82
	case code == 85 || code == 86:
		return render.WeatherSnow, code == 86
	case code >= 95:
		return render.WeatherStorm, true
	default:
		return render.WeatherClouds, false
	}
}

func metSymbolCondition(sym string) (string, bool) {
	s := strings.ToLower(sym)
	switch {
	case strings.Contains(s, "thunder"):
		return render.WeatherStorm, true
	case strings.Contains(s, "snow") || strings.Contains(s, "sleet"):
		return render.WeatherSnow, strings.Contains(s, "heavy")
	case strings.Contains(s, "rain") || strings.Contains(s, "showers"):
		return render.WeatherRain, strings.Contains(s, "heavy")
	case strings.Contains(s, "fog"):
		return render.WeatherFog, false
	case strings.Contains(s, "cloud"):
		return render.WeatherClouds, false
	case strings.Contains(s, "clear") || strings.Contains(s, "fair"):
		return render.WeatherClear, false
	default:
		return render.WeatherClouds, false
	}
}

func wmoOverlay(code int) string {
	switch {
	case code == 48:
		return render.OverlayFrost
	case code >= 51 && code <= 57:
		return render.OverlayDrizzle
	case code == 65 || code == 67 || code == 82:
		return render.OverlayStorm
	case (code >= 61 && code <= 67) || (code >= 80 && code <= 82):
		return render.OverlayRain
	case (code >= 71 && code <= 77) || code == 85 || code == 86:
		return render.OverlaySnow
	case code >= 95:
		return render.OverlayThunder
	default:
		return ""
	}
}

func metSymbolOverlay(sym string) string {
	s := strings.ToLower(sym)
	switch {
	case strings.Contains(s, "thunder"):
		return render.OverlayThunder
	case strings.Contains(s, "snow"):
		return render.OverlaySnow
	case strings.Contains(s, "rain") || strings.Contains(s, "sleet"):
		if strings.Contains(s, "heavy") {
			return render.OverlayStorm
		}
		return render.OverlayRain
	default:
		return ""
	}
}

func (c Config) RefreshEvery() time.Duration {
	return max(time.Duration(c.RefreshMinutes)*time.Minute, c.MinFetchInterval())
}

func (c Config) MinFetchInterval() time.Duration {
	return KeyedProviders[c.Provider].minInterval
}

func (c Config) RetryAfterCap() time.Duration {
	if _, ok := KeyedProviders[c.Provider]; ok {
		return RetryAfterMax
	}
	return c.RefreshEvery()
}

func (c Config) MaxProviderFreshness() time.Duration {
	return max(c.RefreshEvery(), time.Hour)
}

func (c Config) AirWanted() bool { return c.AirTileEnabled() || c.AirPopupThreshold > 0 }

const (
	rainWetMMh       = 0.1
	rainLightMMh     = 2.5
	rainModerateMMh  = 7.6
	rainSpellHorizon = 12 * time.Hour
	rainSpellMerge   = 30 * time.Minute
	RainSpellsMax    = 4
	FaceForecastDays = 3
)

type RainSpell struct {
	Start, End time.Time
	Intensity  int
	Snow       bool
}

func rainIntensity(mmh float64) int {
	switch {
	case mmh < rainWetMMh:
		return 0
	case mmh < rainLightMMh:
		return 1
	case mmh < rainModerateMMh:
		return 2
	default:
		return 3
	}
}

func RainSpells(steps []PrecipStep, step time.Duration, now time.Time) (rainNow int, haveNow bool, until time.Time, spells []RainSpell) {
	type run struct {
		RainSpell
		wet, snowy int
	}
	var runs []run
	for _, p := range steps {
		end := p.At.Add(step)
		if !p.At.After(now) && now.Before(end) {
			rainNow, haveNow = rainIntensity(p.MMh), true
		}
		level := rainIntensity(p.MMh)
		if level == 0 {
			continue
		}
		snow := 0
		if p.Snow {
			snow = 1
		}
		if n := len(runs); n > 0 && !p.At.After(runs[n-1].End.Add(rainSpellMerge)) {
			r := &runs[n-1]
			r.End = end
			r.Intensity = max(r.Intensity, level)
			r.wet++
			r.snowy += snow
			continue
		}
		runs = append(runs, run{RainSpell: RainSpell{Start: p.At, End: end, Intensity: level}, wet: 1, snowy: snow})
	}
	spells = []RainSpell{}
	horizon := now.Add(rainSpellHorizon)
	var edge time.Time
	if n := len(steps); n > 0 {
		edge = steps[n-1].At.Add(step)
	}
	for _, r := range runs {
		if !r.End.After(now) || !r.Start.Before(horizon) || len(spells) == RainSpellsMax {
			continue
		}
		r.Snow = 2*r.snowy > r.wet
		spells = append(spells, r.RainSpell)
		if rainNow > 0 && !r.Start.After(now) && r.End.Before(edge) {
			until = r.End
		}
	}
	return rainNow, haveNow, until, spells
}

func FaceDays(obs Observation, now time.Time) (*Day, []Day) {
	today := -1
	for i, d := range obs.Days {
		if !d.Start.After(now) && now.Before(DayEnd(d)) {
			today = i
		}
	}
	if today < 0 {
		return nil, nil
	}
	t := obs.Days[today]
	t.MinC = minOpt(t.MinC, &obs.TempC)
	t.MaxC = maxOpt(t.MaxC, &obs.TempC)
	next := obs.Days[today+1:]
	return &t, next[:min(len(next), FaceForecastDays)]
}

func boolPtr(b bool) *bool { return &b }

func intPtr(v int) *int { return &v }

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
