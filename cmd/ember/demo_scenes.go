package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/devices"
	"github.com/tarakanof/ember/internal/nowplaying"
)

const (
	demoGroupBot     = "Bot"
	demoGroupMoments = "Weather moments"
	demoGroupFaces   = "Weather faces"
	demoGroupPomo    = "Pomodoro"
	demoGroupNP      = "Now playing"

	demoUTCOffset = time.Hour
	demoCoverPx   = 480
	// Fixed, not the PNG hash: PNG bytes differ between Go releases (ARCHITECTURE "Knob demo mode").
	demoArtHash      = "de30de30de30de30"
	demoLightArtHash = "de30de30de30f0f0"
)

type demoScene struct {
	Name    string `json:"name"`
	Group   string `json:"group"`
	Title   string `json:"title"`
	page    string
	act     string
	offline bool
	view    func(at time.Time, art *demoArt) knobView
}

type demoArt struct {
	entry nowplaying.Entry
	img   *nowplaying.Image
	light *demoArt
}

var demoArtOnce = sync.OnceValues(newDemoArt)

func newDemoArt() (*demoArt, error) {
	from, to := [3]float64{0xF0, 0x6A, 0x2A}, [3]float64{0x5A, 0x2C, 0xD8}
	art, err := prepareDemoArt(demoArtHash, func(x, y int) color.RGBA {
		t := float64(x+y) / float64(2*(demoCoverPx-1))
		mix := func(i int) uint8 { return uint8(from[i] + (to[i]-from[i])*t + 0.5) }
		return color.RGBA{mix(0), mix(1), mix(2), 255}
	})
	if err != nil {
		return nil, err
	}
	if art.light, err = prepareDemoArt(demoLightArtHash, func(x, y int) color.RGBA {
		if x%40 < 3 || y%60 < 3 {
			return color.RGBA{0x22, 0x22, 0x22, 255}
		}
		return color.RGBA{0xF4, 0xEE, 0xE0, 255}
	}); err != nil {
		return nil, err
	}
	return art, nil
}

func prepareDemoArt(hash string, fill func(x, y int) color.RGBA) (*demoArt, error) {
	src := image.NewRGBA(image.Rect(0, 0, demoCoverPx, demoCoverPx))
	for y := range demoCoverPx {
		for x := range demoCoverPx {
			src.SetRGBA(x, y, fill(x, y))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		return nil, fmt.Errorf("encode demo cover: %w", err)
	}
	img, err := nowplaying.Prepare(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("prepare demo cover: %w", err)
	}
	img.Hash = hash
	return &demoArt{entry: nowplaying.Entry{AlbumArt: img}, img: img}, nil
}

func (d *demoArt) picture(kind nowplaying.Kind, v string) (*nowplaying.Image, string, bool) {
	if d == nil || v == "" {
		return nil, "", false
	}
	version := d.entry.ArtVersion()
	if kind == nowplaying.Cover {
		version = d.entry.CoverVersion()
	}
	if v != version {
		return d.light.picture(kind, v)
	}
	return d.entry.Picture(kind), version, true
}

var demoCatalog = buildDemoCatalog()

var demoSceneByName = func() map[string]demoScene {
	m := make(map[string]demoScene, len(demoCatalog))
	for _, s := range demoCatalog {
		m[s.Name] = s
	}
	return m
}()

func buildDemoCatalog() []demoScene {
	var out []demoScene
	out = append(out, demoBotScenes()...)
	out = append(out, demoMomentScenes()...)
	out = append(out, demoFaceScenes()...)
	out = append(out, demoPomoScenes()...)
	out = append(out, demoNPScenes()...)
	return out
}

func moodView(m knobMood) func(time.Time, *demoArt) knobView {
	return func(time.Time, *demoArt) knobView { return knobView{Mood: block(&m)} }
}

func demoBotScenes() []demoScene {
	idle := knobMood{}
	working := knobMood{Running: 1, Source: "studio", LeadColor: "#00C8C8", Tool: "claude"}
	waiting := knobMood{Waiting: 1, Running: 1, Source: "studio", LeadColor: "#00C8C8", Tool: "claude", LeadProject: "learn-language"}
	errored := knobMood{Errors: 1, Source: "studio", LeadColor: "#00C8C8", Tool: "codex"}
	done := knobMood{Done: 1, Source: "studio", LeadColor: "#00C8C8", Tool: "claude", LeadProject: "ember"}
	bot := func(name, title string, m knobMood, act string) demoScene {
		return demoScene{Name: "bot." + name, Group: demoGroupBot, Title: title, page: "bot", act: act, view: moodView(m)}
	}
	offline := bot("offline", "Offline", idle, "")
	offline.offline = true
	calm := waiting
	calm.Waiting = 2
	quiet := demoScene{Name: "bot.quiet_calm", Group: demoGroupBot, Title: "Quiet calm", page: "bot",
		view: func(time.Time, *demoArt) knobView { return knobView{Mood: block(&calm), Quiet: true} }}
	return []demoScene{
		bot("idle", "Idle", idle, ""),
		bot("sleepy", "Sleepy", idle, "sleepy"),
		bot("asleep", "Asleep", idle, "asleep"),
		bot("working", "Working", working, ""),
		bot("waiting", "Waiting", waiting, ""),
		bot("error", "Error", errored, ""),
		bot("done", "Done", done, ""),
		bot("hop", "Hop", waiting, "hop"),
		bot("pop", "Pop", done, "pop"),
		bot("blink", "Blink", idle, "blink"),
		bot("chase_full", "Chase (full)", working, "chase:full"),
		bot("chase_half", "Chase (half)", working, "chase:half"),
		offline,
		quiet,
	}
}

type demoSun struct {
	rise, set time.Duration
	polar     string
}

type demoSpell struct {
	start, end time.Duration
	intensity  int64
	snow       bool
}

type demoSky struct {
	cond, code    string
	tempC         float64
	night, severe bool
	cloud, uv     int
	wind, gust    int
	dir           int
	rain          int
	spells        []demoSpell
	minC, maxC    int
	pop           int
	aqi           int
	sun           demoSun
}

var (
	demoDaySun   = demoSun{rise: -5 * time.Hour, set: 7 * time.Hour}
	demoNightSun = demoSun{rise: -15 * time.Hour, set: -3 * time.Hour}
)

func demoSkies() []struct {
	name, title string
	sky         demoSky
} {
	return []struct {
		name, title string
		sky         demoSky
	}{
		{"clear_day", "clear day", demoSky{cond: "clear", code: "0", tempC: 21.4, cloud: 3, uv: 6, wind: 9, gust: 18, dir: 270,
			minC: 12, maxC: 23, pop: 5, aqi: 24, sun: demoDaySun}},
		{"clear_night", "clear night", demoSky{cond: "clear", code: "0", tempC: 11.2, night: true, cloud: 2, wind: 6, gust: 11, dir: 200,
			minC: 9, maxC: 22, pop: 0, aqi: 18, sun: demoNightSun}},
		{"partly_cloudy", "partly cloudy", demoSky{cond: "clouds", code: "2", tempC: 17.8, cloud: 46, uv: 4, wind: 14, gust: 27, dir: 240,
			minC: 11, maxC: 19, pop: 15, aqi: 31, sun: demoDaySun}},
		{"overcast", "overcast", demoSky{cond: "clouds", code: "3", tempC: 13.1, cloud: 98, uv: 1, wind: 17, gust: 31, dir: 220,
			spells: []demoSpell{{start: 2 * time.Hour, end: 3*time.Hour + 30*time.Minute, intensity: 1}},
			minC:   9, maxC: 14, pop: 55, aqi: 38, sun: demoDaySun}},
		{"fog", "fog", demoSky{cond: "fog", code: "45", tempC: 6.3, cloud: 100, wind: 3, gust: 6, dir: 90,
			minC: 4, maxC: 11, pop: 10, aqi: 72, sun: demoDaySun}},
		{"light_rain", "light rain", demoSky{cond: "rain", code: "61", tempC: 12.6, cloud: 92, uv: 1, wind: 15, gust: 29, dir: 230, rain: 1,
			spells: []demoSpell{{start: -20 * time.Minute, end: 70 * time.Minute, intensity: 1}},
			minC:   9, maxC: 14, pop: 80, aqi: 20, sun: demoDaySun}},
		{"heavy_rain", "heavy rain", demoSky{cond: "rain", code: "65", tempC: 14.9, severe: true, cloud: 100, wind: 28, gust: 52, dir: 210, rain: 3,
			spells: []demoSpell{{start: -30 * time.Minute, end: 50 * time.Minute, intensity: 3},
				{start: 3 * time.Hour, end: 4 * time.Hour, intensity: 2}},
			minC: 11, maxC: 16, pop: 95, aqi: 15, sun: demoDaySun}},
		{"snow", "snow", demoSky{cond: "snow", code: "73", tempC: -1.4, cloud: 100, wind: 12, gust: 24, dir: 20, rain: 2,
			spells: []demoSpell{{start: -time.Hour, end: 2 * time.Hour, intensity: 2, snow: true}},
			minC:   -4, maxC: 1, pop: 90, aqi: 22, sun: demoDaySun}},
		{"storm", "storm", demoSky{cond: "storm", code: "95", tempC: 23.7, severe: true, cloud: 100, uv: 2, wind: 34, gust: 71, dir: 250, rain: 3,
			spells: []demoSpell{{start: -10 * time.Minute, end: 40 * time.Minute, intensity: 3}},
			minC:   17, maxC: 29, pop: 85, aqi: 28, sun: demoDaySun}},
		{"dry", "dry", demoSky{cond: "clouds", code: "1", tempC: 19.5, cloud: 12, uv: 5, wind: 11, gust: 20, dir: 300,
			minC: 10, maxC: 21, pop: 0, aqi: 26, sun: demoDaySun}},
	}
}

func demoMomentSkies() []struct {
	kind string
	sky  demoSky
} {
	byName := map[string]demoSky{}
	for _, s := range demoSkies() {
		byName[s.name] = s.sky
	}
	frost := byName["clear_day"]
	frost.tempC, frost.minC, frost.maxC = -4.2, -7, 1
	heat := byName["clear_day"]
	heat.tempC, heat.minC, heat.maxC, heat.uv = 32.6, 21, 34, 8
	wind := byName["overcast"]
	wind.wind, wind.gust, wind.dir, wind.spells = 48, 76, 250, nil
	after := byName["overcast"]
	after.spells = nil
	return []struct {
		kind string
		sky  demoSky
	}{
		{"glance", byName["partly_cloudy"]},
		{"night", byName["clear_night"]},
		{"frost", frost},
		{"heat", heat},
		{"wind", wind},
		{"afterrain", after},
		{"snow", byName["snow"]},
		{"rain", byName["light_rain"]},
		{"storm", byName["storm"]},
	}
}

func demoMomentScenes() []demoScene {
	var out []demoScene
	working := knobMood{Running: 1, Source: "studio", LeadColor: "#00C8C8", Tool: "claude"}
	for _, m := range demoMomentSkies() {
		for _, variant := range []string{"full", "quiet"} {
			mood := knobMood{}
			if variant == "quiet" {
				mood = working
			}
			sky := m.sky
			out = append(out, demoScene{
				Name:  "moment." + m.kind + "." + variant,
				Group: demoGroupMoments,
				Title: strings.ToUpper(m.kind) + " · " + variant,
				page:  "bot",
				act:   "moment:" + m.kind + ":" + variant,
				view: func(at time.Time, _ *demoArt) knobView {
					return knobView{Mood: block(&mood), Weather: block(demoWeather(at, sky))}
				},
			})
		}
	}
	return out
}

func demoFaceScenes() []demoScene {
	var out []demoScene
	faceView := func(sky demoSky) func(time.Time, *demoArt) knobView {
		return func(at time.Time, _ *demoArt) knobView {
			return knobView{Mood: block(&knobMood{}), Weather: block(demoWeather(at, sky))}
		}
	}
	for _, face := range []string{"temp", "rain", "cast", "wind", "days", "sun", "air"} {
		for _, s := range demoSkies() {
			out = append(out, demoScene{Name: "face." + face + "." + s.name, Group: demoGroupFaces,
				Title: strings.ToUpper(face) + " · " + s.title, page: "weather:" + face, view: faceView(s.sky)})
		}
	}
	base := demoSkies()[0].sky
	phases := []struct {
		name, title string
		sun         demoSun
		night       bool
	}{
		{"night", "night", demoSun{rise: 3 * time.Hour, set: 15 * time.Hour}, true},
		{"dawn", "dawn (blue hour)", demoSun{rise: 25 * time.Minute, set: 12*time.Hour + 25*time.Minute}, true},
		{"golden_morning", "golden hour (morning)", demoSun{rise: -20 * time.Minute, set: 11*time.Hour + 40*time.Minute}, false},
		{"day", "day", demoDaySun, false},
		{"golden_evening", "golden hour (evening)", demoSun{rise: -11*time.Hour - 40*time.Minute, set: 20 * time.Minute}, false},
		{"dusk", "dusk (blue hour)", demoSun{rise: -12*time.Hour - 25*time.Minute, set: -25 * time.Minute}, true},
		{"polar_day", "polar day", demoSun{polar: "day"}, false},
		{"polar_night", "polar night", demoSun{polar: "night"}, true},
	}
	for _, p := range phases {
		sky := base
		sky.sun, sky.night = p.sun, p.night
		if p.night {
			sky.uv, sky.tempC = 0, 8.5
		}
		out = append(out, demoScene{Name: "sun." + p.name, Group: demoGroupFaces, Title: "SUN · " + p.title,
			page: "weather:sun", view: faceView(sky)})
	}
	return out
}

func demoWeather(at time.Time, s demoSky) *knobWeather {
	rel := func(d time.Duration) int64 { return at.Add(d).Unix() }
	relp := func(d time.Duration) *int64 { return new(rel(d)) }
	w := &knobWeather{Provider: "open-meteo", Cond: s.cond, Code: s.code, TempC: s.tempC, Severe: s.severe,
		Night: s.night, ObservedAt: relp(-10 * time.Minute)}
	if s.sun.polar == "" {
		w.Sunrise, w.Sunset = relp(s.sun.rise), relp(s.sun.set)
	}
	d := &knobWeatherDetail{UTCOffset: int64(demoUTCOffset / time.Second)}
	now := knobWxNow{Cloud: new(s.cloud), WindKmh: new(s.wind), GustKmh: new(s.gust), WindDir: new(s.dir), Rain: new(s.rain)}
	if !s.night {
		now.UV = new(s.uv)
	}
	d.Now = &now
	t := int(math.Round(s.tempC))
	d.Today = &knobWxToday{MinC: new(min(s.minC, t)), MaxC: new(max(s.maxC, t)), UVMax: new(max(s.uv, 1)), GustMaxKmh: new(s.gust + 6), Pop: new(s.pop)}
	d.Rain = &knobWxRain{Spells: make([][4]int64, 0, len(s.spells))}
	for _, sp := range s.spells {
		kind := int64(0)
		if sp.snow {
			kind = 1
		}
		d.Rain.Spells = append(d.Rain.Spells, [4]int64{rel(sp.start), rel(sp.end), sp.intensity, kind})
		if s.rain > 0 && sp.start <= 0 && sp.end > 0 {
			d.Rain.Until = relp(sp.end)
		}
	}
	midnight := at.Add(demoUTCOffset).Truncate(24 * time.Hour).Add(-demoUTCOffset)
	days := []struct {
		cond, code      string
		minC, maxC, pop int
		precip          int
	}{
		{s.cond, s.code, s.minC + 1, s.maxC + 2, s.pop, s.pop / 10},
		{"clouds", "3", 8, 15, 30, 0},
		{"rain", "61", 10, 16, 70, 9},
	}
	for i, day := range days {
		start := midnight.Add(time.Duration(i+1) * 24 * time.Hour)
		d.Days = append(d.Days, knobWxDay{T: start.Unix(), End: start.Add(24 * time.Hour).Unix(), Cond: day.cond, Code: day.code,
			MinC: new(day.minC), MaxC: new(day.maxC), Pop: new(day.pop), PrecipMM10: new(day.precip)})
	}
	if s.sun.polar == "" {
		d.Twilight = &knobTwilight{
			Dawn: relp(s.sun.rise - 30*time.Minute), BlueEnd: relp(s.sun.rise - 20*time.Minute),
			GoldenEnd: relp(s.sun.rise + 40*time.Minute), GoldenStart: relp(s.sun.set - 40*time.Minute),
			BlueStart: relp(s.sun.set + 20*time.Minute), Dusk: relp(s.sun.set + 30*time.Minute),
		}
	}
	d.Moon = knobMoon{Phase: 24, Illum: 46, Up: new(s.night)}
	if s.night {
		d.Moon.Rise, d.Moon.Set = relp(-3*time.Hour), relp(5*time.Hour)
	} else {
		d.Moon.Rise, d.Moon.Set = relp(6*time.Hour), relp(18*time.Hour)
	}
	d.AQI = new(s.aqi)
	w.knobWeatherDetail = d
	return w
}

func demoPomoScenes() []demoScene {
	pomo := func(name, title string, offline bool, build func(at time.Time) knobPomo) demoScene {
		return demoScene{Name: "pomodoro." + name, Group: demoGroupPomo, Title: title, page: "pomodoro", offline: offline,
			view: func(at time.Time, _ *demoArt) knobView {
				p := build(at)
				return knobView{Mood: block(&knobMood{}), Pomo: block(&p)}
			}}
	}
	counting := func(phase string, left time.Duration, planned, round int) func(time.Time) knobPomo {
		return func(at time.Time) knobPomo {
			return knobPomo{Phase: phase, Running: true, EndsAt: new(at.Add(left).Unix()), PlannedSec: planned, Round: round}
		}
	}
	return []demoScene{
		pomo("focus", "Focus", false, counting("focus", 18*time.Minute+20*time.Second, 1500, 0)),
		pomo("break", "Break", false, counting("short_break", 3*time.Minute+40*time.Second, 300, 1)),
		pomo("long_break", "Long break", false, counting("long_break", 11*time.Minute, 900, 4)),
		pomo("paused", "Paused", false, func(time.Time) knobPomo {
			return knobPomo{Phase: "focus", Running: true, Paused: true, RemainingSec: new(754), PlannedSec: 1500, Round: 1}
		}),
		pomo("parked", "Parked", false, func(time.Time) knobPomo {
			return knobPomo{Phase: "short_break", RemainingSec: new(300), PlannedSec: 300, Round: 1}
		}),
		pomo("done", "3 done", false, counting("focus", 21*time.Minute, 1500, 3)),
		pomo("offline", "Offline", true, counting("focus", 18*time.Minute+20*time.Second, 1500, 0)),
	}
}

func demoNPScenes() []demoScene {
	np := func(name, title string, offline, light bool, state string) demoScene {
		return demoScene{Name: "nowplaying." + name, Group: demoGroupNP, Title: title, page: devices.KnobNowPlayingPage, offline: offline,
			view: func(at time.Time, art *demoArt) knobView {
				v := knobView{Mood: block(&knobMood{}), NowPlaying: &knobNowPlaying{State: nowPlayingNone}}
				if state == nowPlayingNone {
					return v
				}
				v.NowPlaying = &knobNowPlaying{State: state, Source: "demo", Title: "Ember Demo Track", Artist: "The Gradients",
					Album: "Test Pattern", TrackID: "demo", DurationMS: 214_000, PositionMS: 63_000, PositionAt: at.UnixMilli(),
					Volume: new(45)}
				if light && art != nil {
					art = art.light
				}
				if art != nil {
					v.NowPlaying.ArtVersion = art.entry.ArtVersion()
					v.NowPlaying.AlbumArt = true
					v.NowPlaying.Accent = art.img.Accent
					v.NowPlaying.CoverVersion = art.entry.CoverVersion()
					v.NowPlaying.CoverLight = art.img.CoverLight
				}
				return v
			}}
	}
	return []demoScene{
		np("playing", "Playing", false, false, string(nowplaying.Playing)),
		np("playing_light", "Light cover", false, true, string(nowplaying.Playing)),
		np("paused", "Paused", false, false, string(nowplaying.Paused)),
		np("nothing", "Nothing playing", false, false, nowPlayingNone),
		np("offline", "Offline", true, false, string(nowplaying.Playing)),
	}
}
