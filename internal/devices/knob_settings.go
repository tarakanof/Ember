package devices

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	KnobNowPlayingPage = "nowplaying"
)

var KnobPageIDs = []string{"bot", "day", "weather", "room", "nowplaying", "controls", "pomodoro"}

// knobMaxPages is the firmware's KS_MAX_PAGES: it drops pages past the 8th.
const KnobMaxPages = 8

var knobPagesOffByDefault = []string{"room", "nowplaying", "controls", "pomodoro"}

var knobPageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,15}$`)

type KnobSettings struct {
	Brightness     KnobBrightness `json:"brightness"`
	Pages          []KnobPage     `json:"pages"`
	Home           string         `json:"home"`
	SwipePages     *bool          `json:"swipe_pages,omitempty"`
	PollMS         int            `json:"poll_ms"`
	Bot            knobBot        `json:"bot"`
	Diagnostics    string         `json:"diagnostics"`
	StatsIntervalS int            `json:"stats_interval_s"`
	LiveIntervalS  int            `json:"live_interval_s"`
	Display        knobDisplay    `json:"display"`
	Quiet          knobQuiet      `json:"quiet"`
	Idle           *KnobIdle      `json:"idle,omitempty"`
	Apps           *KnobApps      `json:"apps,omitempty"`
	Room           *KnobRoom      `json:"room,omitempty"`
}

type KnobIdle struct {
	DimAfterS *int `json:"dim_after_s,omitempty"`
	DimLevel  *int `json:"dim_level,omitempty"`
	OffAfterS *int `json:"off_after_s,omitempty"`
}

type KnobRoom struct {
	Enabled      *bool `json:"enabled,omitempty"`
	PresenceOffS *int  `json:"presence_off_s,omitempty"`
	PresenceWake *bool `json:"presence_wake,omitempty"`
	Moments      *bool `json:"moments,omitempty"`
}

const knobRoomPresenceOffMax = 86399

type KnobApps struct {
	Pomodoro   *KnobPomoApp    `json:"pomodoro,omitempty"`
	Weather    *KnobWeatherApp `json:"weather,omitempty"`
	NowPlaying *KnobNPApp      `json:"nowplaying,omitempty"`
}

type KnobPomoApp struct {
	FocusColor *string `json:"focus_color,omitempty"`
	BreakColor *string `json:"break_color,omitempty"`
}

type KnobWeatherApp struct {
	Faces []string `json:"faces,omitempty"`
}

type KnobNPApp struct {
	DimAfterS *int `json:"dim_after_s,omitempty"`
}

var KnobWeatherFaces = []string{"temp", "rain", "cast", "wind", "days", "sun", "air"}

var knobColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (i *KnobIdle) clone() *KnobIdle {
	if i == nil {
		return nil
	}
	return &KnobIdle{DimAfterS: clonePtr(i.DimAfterS), DimLevel: clonePtr(i.DimLevel), OffAfterS: clonePtr(i.OffAfterS)}
}

func (r *KnobRoom) clone() *KnobRoom {
	if r == nil {
		return nil
	}
	return &KnobRoom{Enabled: clonePtr(r.Enabled), PresenceOffS: clonePtr(r.PresenceOffS),
		PresenceWake: clonePtr(r.PresenceWake), Moments: clonePtr(r.Moments)}
}

func (a *KnobApps) clone() *KnobApps {
	if a == nil {
		return nil
	}
	out := &KnobApps{}
	if a.Pomodoro != nil {
		out.Pomodoro = &KnobPomoApp{FocusColor: clonePtr(a.Pomodoro.FocusColor), BreakColor: clonePtr(a.Pomodoro.BreakColor)}
	}
	if a.Weather != nil {
		out.Weather = &KnobWeatherApp{Faces: slices.Clone(a.Weather.Faces)}
	}
	if a.NowPlaying != nil {
		out.NowPlaying = &KnobNPApp{DimAfterS: clonePtr(a.NowPlaying.DimAfterS)}
	}
	return out
}

func (s *KnobSettings) pruneEmptyV2() {
	if i := s.Idle; i != nil && i.DimAfterS == nil && i.DimLevel == nil && i.OffAfterS == nil {
		s.Idle = nil
	}
	if r := s.Room; r != nil && r.Enabled == nil && r.PresenceOffS == nil && r.PresenceWake == nil && r.Moments == nil {
		s.Room = nil
	}
	a := s.Apps
	if a == nil {
		return
	}
	if p := a.Pomodoro; p != nil && p.FocusColor == nil && p.BreakColor == nil {
		a.Pomodoro = nil
	}
	if w := a.Weather; w != nil && w.Faces == nil {
		a.Weather = nil
	}
	if n := a.NowPlaying; n != nil && n.DimAfterS == nil {
		a.NowPlaying = nil
	}
	if a.Pomodoro == nil && a.Weather == nil && a.NowPlaying == nil {
		s.Apps = nil
	}
}

func (s *KnobSettings) normalizeColors() {
	if s.Apps == nil || s.Apps.Pomodoro == nil {
		return
	}
	for _, c := range []*string{s.Apps.Pomodoro.FocusColor, s.Apps.Pomodoro.BreakColor} {
		if c != nil {
			*c = strings.ToUpper(*c)
		}
	}
}

func (s KnobSettings) validateIdle() error {
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

func (s KnobSettings) validateRoom() error {
	if s.Room == nil || s.Room.PresenceOffS == nil {
		return nil
	}
	return inRange("room.presence_off_s", *s.Room.PresenceOffS, 0, knobRoomPresenceOffMax)
}

func (s KnobSettings) validateApps() error {
	a := s.Apps
	if a == nil {
		return nil
	}
	if p := a.Pomodoro; p != nil {
		for _, f := range []struct {
			name string
			v    *string
		}{{"apps.pomodoro.focus_color", p.FocusColor}, {"apps.pomodoro.break_color", p.BreakColor}} {
			if f.v != nil && !knobColorPattern.MatchString(*f.v) {
				return fmt.Errorf("%s %q must match %s", f.name, *f.v, knobColorPattern)
			}
		}
	}
	if w := a.Weather; w != nil && w.Faces != nil {
		if len(w.Faces) < 1 || len(w.Faces) > len(KnobWeatherFaces) {
			return fmt.Errorf("apps.weather.faces lists %d, want 1-%d", len(w.Faces), len(KnobWeatherFaces))
		}
		for i, f := range w.Faces {
			if !slices.Contains(KnobWeatherFaces, f) {
				return fmt.Errorf("apps.weather.faces %q must be one of %v", f, KnobWeatherFaces)
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

const KnobQuietDimDefault = 20

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
		v := KnobQuietDimDefault
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
	KnobStatsIntervalDefault = 60
	KnobLiveIntervalDefault  = 5
)

const (
	KnobDiagOff   = "off"
	KnobDiagBasic = "basic"
	KnobDiagFull  = "full"
)

type KnobBrightness struct {
	FollowEmber bool `json:"follow_ember"`
	Level       int  `json:"level"`
	Floor       int  `json:"floor"`
	Startup     int  `json:"startup"`
}

func (s KnobSettings) PageOn(id string) bool {
	for _, p := range s.Pages {
		if p.ID == id {
			return p.On
		}
	}
	return false
}

type KnobPage struct {
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

func (s KnobSettings) clone() KnobSettings {
	s.Pages = slices.Clone(s.Pages)
	s.Bot = s.Bot.clone()
	s.Display = s.Display.clone()
	s.Quiet = s.Quiet.clone()
	s.Idle = s.Idle.clone()
	s.Apps = s.Apps.clone()
	s.Room = s.Room.clone()
	if s.SwipePages != nil {
		v := *s.SwipePages
		s.SwipePages = &v
	}
	return s
}

func DefaultKnobSettings() KnobSettings {
	pages := make([]KnobPage, 0, len(KnobPageIDs))
	for _, id := range KnobPageIDs {
		pages = append(pages, KnobPage{ID: id, On: !slices.Contains(knobPagesOffByDefault, id)})
	}
	return KnobSettings{
		Brightness: KnobBrightness{FollowEmber: true, Level: 153, Floor: 10, Startup: 153},
		Pages:      pages,
		Home:       "bot",
		SwipePages: boolPtr(true),
		PollMS:     2000,
		Bot: knobBot{SleepyAfterS: 300, DemoHoldS: 20, SourceLabel: boolPtr(true), WorkingRing: boolPtr(true),
			WeatherMoments: boolPtr(true), WeatherEveryMin: new(knobWeatherEveryMinDefault)},
		Display:     knobDisplay{FastLink: boolPtr(true)},
		Quiet:       knobQuiet{Calm: boolPtr(true), DimLevel: new(KnobQuietDimDefault)},
		Diagnostics: KnobDiagOff,

		StatsIntervalS: KnobStatsIntervalDefault,
		LiveIntervalS:  KnobLiveIntervalDefault,
	}
}

func (s *KnobSettings) FillDefaults() {
	if s.Diagnostics == "" {
		s.Diagnostics = KnobDiagOff
	}
	if s.StatsIntervalS == 0 {
		s.StatsIntervalS = KnobStatsIntervalDefault
	}
	if s.LiveIntervalS == 0 {
		s.LiveIntervalS = KnobLiveIntervalDefault
	}
	s.fillFlagDefaults()
	s.addKnownPages()
}

func (s *KnobSettings) fillFlagDefaults() {
	if s.SwipePages == nil {
		s.SwipePages = boolPtr(true)
	}
	s.Bot.fillDefaults()
	s.Display.fillDefaults()
	s.Quiet.fillDefaults()
}

func (s *KnobSettings) addKnownPages() {
	if len(s.Pages) == 0 {
		return
	}
	for _, id := range KnobPageIDs {
		if len(s.Pages) >= KnobMaxPages {
			return
		}
		if !slices.ContainsFunc(s.Pages, func(p KnobPage) bool { return p.ID == id }) {
			s.Pages = append(s.Pages, KnobPage{ID: id, On: false})
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

func (s KnobSettings) Validate() error {
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
	case KnobDiagOff, KnobDiagBasic, KnobDiagFull:
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
	if err := s.validateRoom(); err != nil {
		return err
	}
	return s.validateApps()
}

func (s KnobSettings) validatePages() error {
	if len(s.Pages) > KnobMaxPages {
		return fmt.Errorf("pages: %d listed, the knob shows at most %d", len(s.Pages), KnobMaxPages)
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

var ErrSettingBody = errors.New("invalid settings body")

var ErrSettingNotObject = fmt.Errorf("%w: must be a JSON object", ErrSettingBody)

func boolPtr(b bool) *bool { return &b }
