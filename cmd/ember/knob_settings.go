package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var knobPageIDs = []string{"bot", "pomodoro", "weather", "nowplaying"}

// knobMaxPages is the firmware's KS_MAX_PAGES: it drops pages past the 8th.
const knobMaxPages = 8

var knobPagesOffByDefault = []string{"nowplaying"}

var knobPageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,15}$`)

type knobSettings struct {
	Brightness     knobBrightness `json:"brightness"`
	Pages          []knobPage     `json:"pages"`
	Home           string         `json:"home"`
	SwipePages     *bool          `json:"swipe_pages,omitempty"`
	PollMS         int            `json:"poll_ms"`
	Bot            knobBot        `json:"bot"`
	Diagnostics    string         `json:"diagnostics"`
	StatsIntervalS int            `json:"stats_interval_s"`
	LiveIntervalS  int            `json:"live_interval_s"`
	Display        knobDisplay    `json:"display"`
	Quiet          knobQuiet      `json:"quiet"`
	Idle           *knobIdle      `json:"idle,omitempty"`
	Apps           *knobApps      `json:"apps,omitempty"`
}

type knobIdle struct {
	DimAfterS *int `json:"dim_after_s,omitempty"`
	DimLevel  *int `json:"dim_level,omitempty"`
	OffAfterS *int `json:"off_after_s,omitempty"`
}

type knobApps struct {
	Pomodoro   *knobPomoApp    `json:"pomodoro,omitempty"`
	Weather    *knobWeatherApp `json:"weather,omitempty"`
	NowPlaying *knobNPApp      `json:"nowplaying,omitempty"`
}

type knobPomoApp struct {
	FocusColor *string `json:"focus_color,omitempty"`
	BreakColor *string `json:"break_color,omitempty"`
}

type knobWeatherApp struct {
	Faces []string `json:"faces,omitempty"`
}

type knobNPApp struct {
	DimAfterS *int `json:"dim_after_s,omitempty"`
}

var knobWeatherFaces = []string{"temp", "rain", "cast", "wind", "days", "sun", "air"}

var knobColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (i *knobIdle) clone() *knobIdle {
	if i == nil {
		return nil
	}
	return &knobIdle{DimAfterS: clonePtr(i.DimAfterS), DimLevel: clonePtr(i.DimLevel), OffAfterS: clonePtr(i.OffAfterS)}
}

func (a *knobApps) clone() *knobApps {
	if a == nil {
		return nil
	}
	out := &knobApps{}
	if a.Pomodoro != nil {
		out.Pomodoro = &knobPomoApp{FocusColor: clonePtr(a.Pomodoro.FocusColor), BreakColor: clonePtr(a.Pomodoro.BreakColor)}
	}
	if a.Weather != nil {
		out.Weather = &knobWeatherApp{Faces: slices.Clone(a.Weather.Faces)}
	}
	if a.NowPlaying != nil {
		out.NowPlaying = &knobNPApp{DimAfterS: clonePtr(a.NowPlaying.DimAfterS)}
	}
	return out
}

func (s *knobSettings) normalizeColors() {
	if s.Apps == nil || s.Apps.Pomodoro == nil {
		return
	}
	for _, c := range []*string{s.Apps.Pomodoro.FocusColor, s.Apps.Pomodoro.BreakColor} {
		if c != nil {
			*c = strings.ToUpper(*c)
		}
	}
}

func (s knobSettings) validateIdle() error {
	i := s.Idle
	if i == nil {
		return nil
	}
	if i.DimAfterS != nil && *i.DimAfterS != 0 {
		if err := inRange("idle.dim_after_s", *i.DimAfterS, 30, 86400); err != nil {
			return err
		}
	}
	if i.DimLevel != nil {
		if err := inRange("idle.dim_level", *i.DimLevel, 1, 255); err != nil {
			return err
		}
	}
	if i.OffAfterS != nil && *i.OffAfterS != 0 {
		if err := inRange("idle.off_after_s", *i.OffAfterS, 60, 86400); err != nil {
			return err
		}
		if i.DimAfterS != nil && *i.DimAfterS != 0 && *i.OffAfterS <= *i.DimAfterS {
			return fmt.Errorf("idle.off_after_s %d must be > idle.dim_after_s %d", *i.OffAfterS, *i.DimAfterS)
		}
	}
	return nil
}

func (s knobSettings) validateApps() error {
	a := s.Apps
	if a == nil {
		return nil
	}
	if p := a.Pomodoro; p != nil {
		for field, c := range map[string]*string{"apps.pomodoro.focus_color": p.FocusColor, "apps.pomodoro.break_color": p.BreakColor} {
			if c != nil && !knobColorPattern.MatchString(*c) {
				return fmt.Errorf("%s %q must match %s", field, *c, knobColorPattern)
			}
		}
	}
	if w := a.Weather; w != nil && w.Faces != nil {
		if len(w.Faces) < 1 || len(w.Faces) > len(knobWeatherFaces) {
			return fmt.Errorf("apps.weather.faces lists %d, want 1-%d", len(w.Faces), len(knobWeatherFaces))
		}
		for i, f := range w.Faces {
			if !slices.Contains(knobWeatherFaces, f) {
				return fmt.Errorf("apps.weather.faces %q must be one of %v", f, knobWeatherFaces)
			}
			if slices.Contains(w.Faces[:i], f) {
				return fmt.Errorf("apps.weather.faces lists %q twice", f)
			}
		}
	}
	if n := a.NowPlaying; n != nil && n.DimAfterS != nil {
		if err := inRange("apps.nowplaying.dim_after_s", *n.DimAfterS, 60, 1800); err != nil {
			return err
		}
	}
	return nil
}

type knobQuiet struct {
	Calm     *bool `json:"calm,omitempty"`
	DimLevel *int  `json:"dim_level,omitempty"`
}

const knobQuietDimDefault = brightnessNightLevelDefault

func (q knobQuiet) clone() knobQuiet {
	if q.Calm != nil {
		v := *q.Calm
		q.Calm = &v
	}
	if q.DimLevel != nil {
		v := *q.DimLevel
		q.DimLevel = &v
	}
	return q
}

func (q *knobQuiet) fillDefaults() {
	if q.Calm == nil {
		q.Calm = boolPtr(true)
	}
	if q.DimLevel == nil {
		v := knobQuietDimDefault
		q.DimLevel = &v
	}
}

type knobDisplay struct {
	// Pointer: a record stored without it reads as on.
	FastLink *bool `json:"fast_link,omitempty"`
	Rotation int   `json:"rotation"`
}

func (d knobDisplay) clone() knobDisplay {
	if d.FastLink != nil {
		v := *d.FastLink
		d.FastLink = &v
	}
	return d
}

var (
	knobStatsIntervals = []int{30, 60, 120, 300}
	knobLiveIntervals  = []int{2, 5, 10}
	knobRotations      = []int{0, 90, 180, 270}
)

const (
	knobStatsIntervalDefault = 60
	knobLiveIntervalDefault  = 5
)

const (
	knobDiagOff   = "off"
	knobDiagBasic = "basic"
	knobDiagFull  = "full"
)

type knobBrightness struct {
	FollowEmber bool `json:"follow_ember"`
	Level       int  `json:"level"`
	Floor       int  `json:"floor"`
	Startup     int  `json:"startup"`
}

func (s knobSettings) pageOn(id string) bool {
	for _, p := range s.Pages {
		if p.ID == id {
			return p.On
		}
	}
	return false
}

type knobPage struct {
	ID string `json:"id"`
	On bool   `json:"on"`
}

type knobBot struct {
	SleepyAfterS int `json:"sleepy_after_s"`
	DemoHoldS    int `json:"demo_hold_s"`
	// Pointers: a record stored before them reads as on.
	SourceLabel     *bool `json:"source_label,omitempty"`
	WorkingRing     *bool `json:"working_ring,omitempty"`
	WeatherMoments  *bool `json:"weather_moments,omitempty"`
	WeatherEveryMin *int  `json:"weather_every_min,omitempty"`
}

const (
	knobWeatherEveryMinDefault = 30
	knobWeatherEveryMinMax     = 240
)

// Copies the flag pointers so a decoded patch never writes through the stored record.
func (b knobBot) clone() knobBot {
	if b.SourceLabel != nil {
		v := *b.SourceLabel
		b.SourceLabel = &v
	}
	if b.WorkingRing != nil {
		v := *b.WorkingRing
		b.WorkingRing = &v
	}
	if b.WeatherMoments != nil {
		v := *b.WeatherMoments
		b.WeatherMoments = &v
	}
	if b.WeatherEveryMin != nil {
		v := *b.WeatherEveryMin
		b.WeatherEveryMin = &v
	}
	return b
}

func (s knobSettings) clone() knobSettings {
	s.Pages = slices.Clone(s.Pages)
	s.Bot = s.Bot.clone()
	s.Display = s.Display.clone()
	s.Quiet = s.Quiet.clone()
	s.Idle = s.Idle.clone()
	s.Apps = s.Apps.clone()
	if s.SwipePages != nil {
		v := *s.SwipePages
		s.SwipePages = &v
	}
	return s
}

func defaultKnobSettings() knobSettings {
	pages := make([]knobPage, 0, len(knobPageIDs))
	for _, id := range knobPageIDs {
		pages = append(pages, knobPage{ID: id, On: !slices.Contains(knobPagesOffByDefault, id)})
	}
	return knobSettings{
		Brightness: knobBrightness{FollowEmber: true, Level: 153, Floor: 10, Startup: 153},
		Pages:      pages,
		Home:       "bot",
		SwipePages: boolPtr(true),
		PollMS:     2000,
		Bot: knobBot{SleepyAfterS: 300, DemoHoldS: 20, SourceLabel: boolPtr(true), WorkingRing: boolPtr(true),
			WeatherMoments: boolPtr(true), WeatherEveryMin: new(knobWeatherEveryMinDefault)},
		Display:     knobDisplay{FastLink: boolPtr(true)},
		Quiet:       knobQuiet{Calm: boolPtr(true), DimLevel: new(knobQuietDimDefault)},
		Diagnostics: knobDiagOff,

		StatsIntervalS: knobStatsIntervalDefault,
		LiveIntervalS:  knobLiveIntervalDefault,
	}
}

func (s *knobSettings) fillDefaults() {
	if s.Diagnostics == "" {
		s.Diagnostics = knobDiagOff
	}
	if s.StatsIntervalS == 0 {
		s.StatsIntervalS = knobStatsIntervalDefault
	}
	if s.LiveIntervalS == 0 {
		s.LiveIntervalS = knobLiveIntervalDefault
	}
	s.fillFlagDefaults()
	s.addKnownPages()
}

func (s *knobSettings) fillFlagDefaults() {
	if s.SwipePages == nil {
		s.SwipePages = boolPtr(true)
	}
	s.Bot.fillDefaults()
	s.Display.fillDefaults()
	s.Quiet.fillDefaults()
}

func (s *knobSettings) addKnownPages() {
	if len(s.Pages) == 0 {
		return
	}
	for _, id := range knobPageIDs {
		if len(s.Pages) >= knobMaxPages {
			return
		}
		if !slices.ContainsFunc(s.Pages, func(p knobPage) bool { return p.ID == id }) {
			s.Pages = append(s.Pages, knobPage{ID: id, On: false})
		}
	}
}

func (d *knobDisplay) fillDefaults() {
	if d.FastLink == nil {
		d.FastLink = boolPtr(true)
	}
}

func (b *knobBot) fillDefaults() {
	if b.SourceLabel == nil {
		b.SourceLabel = boolPtr(true)
	}
	if b.WorkingRing == nil {
		b.WorkingRing = boolPtr(true)
	}
	if b.WeatherMoments == nil {
		b.WeatherMoments = boolPtr(true)
	}
	if b.WeatherEveryMin == nil {
		b.WeatherEveryMin = new(knobWeatherEveryMinDefault)
	}
}

func (s knobSettings) validate() error {
	b := s.Brightness
	if err := inRange("brightness.level", b.Level, 0, 255); err != nil {
		return err
	}
	if err := inRange("brightness.floor", b.Floor, 1, 255); err != nil {
		return err
	}
	if b.Floor > b.Level {
		return fmt.Errorf("brightness.floor %d must be <= brightness.level %d", b.Floor, b.Level)
	}
	if err := inRange("brightness.startup", b.Startup, 0, 255); err != nil {
		return err
	}
	if err := s.validatePages(); err != nil {
		return err
	}
	if err := inRange("poll_ms", s.PollMS, 1000, 10000); err != nil {
		return err
	}
	switch s.Diagnostics {
	case knobDiagOff, knobDiagBasic, knobDiagFull:
	default:
		return fmt.Errorf("diagnostics %q must be off, basic or full", s.Diagnostics)
	}
	if err := oneOf("stats_interval_s", s.StatsIntervalS, knobStatsIntervals); err != nil {
		return err
	}
	if err := oneOf("live_interval_s", s.LiveIntervalS, knobLiveIntervals); err != nil {
		return err
	}
	if err := oneOf("display.rotation", s.Display.Rotation, knobRotations); err != nil {
		return err
	}
	if err := inRange("bot.sleepy_after_s", s.Bot.SleepyAfterS, 0, 86400); err != nil {
		return err
	}
	if err := inRange("bot.demo_hold_s", s.Bot.DemoHoldS, 1, 600); err != nil {
		return err
	}
	if s.Bot.WeatherEveryMin != nil {
		if err := inRange("bot.weather_every_min", *s.Bot.WeatherEveryMin, 0, knobWeatherEveryMinMax); err != nil {
			return err
		}
	}
	if s.Quiet.DimLevel != nil {
		if err := inRange("quiet.dim_level", *s.Quiet.DimLevel, 1, 255); err != nil {
			return err
		}
	}
	if err := s.validateIdle(); err != nil {
		return err
	}
	return s.validateApps()
}

func (s knobSettings) validatePages() error {
	if len(s.Pages) > knobMaxPages {
		return fmt.Errorf("pages: %d listed, the knob shows at most %d", len(s.Pages), knobMaxPages)
	}
	seen := make(map[string]bool, len(s.Pages))
	homeOn := false
	for _, p := range s.Pages {
		if !knobPageIDPattern.MatchString(p.ID) {
			return fmt.Errorf("page id %q must match %s", p.ID, knobPageIDPattern)
		}
		if seen[p.ID] {
			return fmt.Errorf("page %q listed twice", p.ID)
		}
		seen[p.ID] = true
		if p.ID == s.Home && p.On {
			homeOn = true
		}
	}
	if !homeOn {
		return errors.New("home must name a page that is on")
	}
	return nil
}

func oneOf(field string, v int, allowed []int) error {
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("%s %d must be one of %v", field, v, allowed)
	}
	return nil
}

func inRange(field string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s %d out of range [%d, %d]", field, v, lo, hi)
	}
	return nil
}
