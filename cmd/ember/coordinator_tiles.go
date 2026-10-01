package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
	"github.com/tarakanof/ember/internal/render"
)

type tileInputs struct {
	now time.Time

	weather WeatherConfig
	obs     weatherObservation
	haveObs bool
	air     airObservation
	haveAir bool

	meet      MeetingsConfig
	nextMeet  meetings.Occurrence
	haveMeet  bool
	meetFresh bool
}

type tileView struct {
	payload map[string]any
	frame   func() render.Frame
}

type tile struct {
	app    string
	card   string
	toggle func(in *tileInputs) bool
	live   func(in *tileInputs) bool
	view   func(in *tileInputs) (tileView, bool)
}

var tiles = []tile{weatherTile, forecastTile, airTile, meetTile}

const legacyUsagePrefix = "ember-usage-"

type tileWriter interface {
	push(app string, payload map[string]any) error
	clear(app string) error
}

type pushedApp struct {
	body []byte
	at   time.Time
}

func (p pushedApp) current(body []byte, now time.Time, window time.Duration) bool {
	return p.body != nil && bytes.Equal(p.body, body) && now.Sub(p.at) < window
}

type tileSet struct {
	pushed map[string]pushedApp
	logger *slog.Logger
}

func (s *tileSet) adopt(deviceApps []string, baseApp string) {
	for _, name := range deviceApps {
		if name == baseApp {
			continue
		}
		if !isTileApp(name) && !strings.HasPrefix(name, legacyUsagePrefix) {
			continue
		}
		if s.pushed == nil {
			s.pushed = map[string]pushedApp{}
		}
		if _, ok := s.pushed[name]; !ok {
			s.pushed[name] = pushedApp{}
		}
	}
}

func (s *tileSet) forget() { s.pushed = nil }

func (s *tileSet) reconcile(in tileInputs, w tileWriter) {
	legacy := make([]string, 0, len(s.pushed))
	for name := range s.pushed {
		if !isTileApp(name) {
			legacy = append(legacy, name)
		}
	}
	slices.Sort(legacy)
	for _, name := range legacy {
		s.clear(w, name)
	}
	for i := range tiles {
		t := &tiles[i]
		v, want := t.decide(&in)
		if !want {
			s.clear(w, t.app)
			continue
		}
		s.push(w, t.app, v.payload, in.now)
	}
}

func (s *tileSet) clear(w tileWriter, app string) {
	if _, tracked := s.pushed[app]; !tracked {
		return
	}
	if err := w.clear(app); err != nil {
		s.log().Warn("tile clear failed", "app", app, "err", err)
		return
	}
	delete(s.pushed, app)
}

func (s *tileSet) push(w tileWriter, app string, payload map[string]any, now time.Time) {
	body, err := json.Marshal(payload)
	if err != nil {
		s.log().Warn("tile payload marshal failed", "app", app, "err", err)
		return
	}
	if s.pushed[app].current(body, now, usageRefreshInterval) {
		return
	}
	if err := w.push(app, payload); err != nil {
		s.log().Warn("tile publish failed", "app", app, "err", err)
		return
	}
	if s.pushed == nil {
		s.pushed = map[string]pushedApp{}
	}
	s.pushed[app] = pushedApp{body: body, at: now}
}

func (s *tileSet) log() *slog.Logger {
	if s.logger == nil {
		return slog.Default()
	}
	return s.logger
}

func (t *tile) decide(in *tileInputs) (tileView, bool) {
	if !t.toggle(in) || !t.live(in) {
		return tileView{}, false
	}
	return t.view(in)
}

func isTileApp(name string) bool {
	return slices.ContainsFunc(tiles, func(t tile) bool { return t.app == name })
}

func previewTiles(in tileInputs, cards ...string) render.Preview {
	p := render.Preview{Width: 32, Height: 8, Frames: []render.CardFrame{}}
	for i := range tiles {
		t := &tiles[i]
		if !slices.Contains(cards, t.card) || !t.toggle(&in) {
			continue
		}
		v, ok := t.view(&in)
		if !ok {
			continue
		}
		frame := v.frame()
		p.Frames = append(p.Frames, render.CardFrame{Card: t.card, Pixels: render.HexPixels(&frame)})
	}
	return p
}

func (c *coordinator) tileInputs(now time.Time) tileInputs {
	cfg := c.loadCfg()
	in := tileInputs{now: now, weather: cfg.Weather, meet: cfg.Meetings}
	if c.weather != nil {
		in.obs, in.haveObs = c.weather.current()
		in.air, in.haveAir = c.weather.currentAir()
	}
	if c.meetings != nil {
		in.nextMeet, in.haveMeet = c.meetings.next(now)
		in.meetFresh = c.meetings.fresh(now)
	}
	return in
}

func (c *coordinator) reconcileTiles(now time.Time) {
	c.tiles.reconcile(c.tileInputs(now), coordTileWriter{c})
}

func (c *coordinator) adoptDeviceManagedApps() bool {
	names, err := c.publisher.ListApps(c.runCtx())
	if err != nil {
		c.logger.Warn("device app loop read failed; deferring adopt", "err", err)
		return false
	}
	c.tiles.adopt(names, c.loadCfg().AWTRIX.AppName)
	return true
}

type coordTileWriter struct{ c *coordinator }

func (w coordTileWriter) push(app string, payload map[string]any) error {
	return w.c.pushApp(app, payload)
}

func (w coordTileWriter) clear(app string) error {
	return w.c.publisher.ClearApp(w.c.runCtx(), app)
}
