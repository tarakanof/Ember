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

// The tile module: every standalone rotating app Ember owns on the device
// (ember-weather, ember-forecast, ember-air, ember-meet) is one entry in
// tiles, and both the coordinator and the preview endpoints read that entry.
// The coordinator asks tileSet.reconcile to converge the device, and the
// previews ask previewTiles for the same view's frame, so a preview is the
// pushed frame by construction (the NG overlay and native gallery icon are
// payload-only: the canvas can't animate them).
//
// Adding a tile means adding one tile value to tiles; the ledger, adopt,
// republish reset, tick and preview plumbing need no edit.

// tileInputs is everything a tile reads: settings, store readings and the
// instant. The coordinator fills it from the live config and stores
// (coordinator.tileInputs); a preview fills it from a draft config and sample
// data.
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

// tileView is one tile's content, resolved once: the payload the device gets
// and the frame a preview draws.
type tileView struct {
	payload map[string]any
	frame   render.Frame
}

// tile is one standalone rotating app.
type tile struct {
	app  string // device app name
	card string // preview card name
	// toggle is the tile's own display switch; the preview honours it too.
	toggle func(in *tileInputs) bool
	// live is the device-only gate: feature enabled, data fresh, in window.
	// Previews skip it, since they show what the tile would look like.
	live func(in *tileInputs) bool
	// view builds the content; false means there is nothing to show (e.g. no
	// hourly data), which keeps the tile off the device and out of a preview.
	view func(in *tileInputs) (tileView, bool)
}

// tiles is every standalone rotating tile, in push and preview order.
var tiles = []tile{weatherTile, forecastTile, airTile, meetTile}

// legacyUsagePrefix names the standalone ember-usage-* apps an older server
// pushed. Usage now renders inside the main app, so adopt seeds any it finds
// and reconcile clears them.
const legacyUsagePrefix = "ember-usage-"

// tileWriter is the device side of reconcile. The coordinator's adapter
// (coordTileWriter) routes through its own push retry policy and publisher, so
// the coordinator still does every device write, on its own goroutine.
type tileWriter interface {
	push(app string, payload map[string]any) error
	clear(app string) error
}

// pushedApp records the payload bytes and time of an app's last successful
// push, for change-and-staleness-aware re-push. The zero value means "on the
// device, content unknown" (adopted after a restart): never current, so the
// next reconcile re-pushes or clears it.
type pushedApp struct {
	body []byte
	at   time.Time
}

// current reports whether body is what was last pushed, less than window ago.
func (p pushedApp) current(body []byte, now time.Time, window time.Duration) bool {
	return p.body != nil && bytes.Equal(p.body, body) && now.Sub(p.at) < window
}

// tileSet owns the pushed-app ledger for the tiles and the legacy usage
// leftovers. Coordinator goroutine only; the zero value is ready to use.
type tileSet struct {
	pushed map[string]pushedApp
	logger *slog.Logger
}

// adopt seeds the ledger from the apps actually on the device, so tiles (and
// legacy usage apps) left there by a previous process are reconciled, and
// cleared when no longer wanted, even though the ledger starts empty after a
// restart. The base rotating app and native apps are left alone.
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

// forget drops the ledger: after a device reboot nothing we pushed is there,
// and a stale ledger would suppress the re-push for a whole refresh interval.
func (s *tileSet) forget() { s.pushed = nil }

// reconcile converges the device on in: legacy usage leftovers are cleared,
// then each tile is pushed when wanted and changed (or due for a refresh),
// and cleared when unwanted but on the device. Only a successful write moves
// the ledger, so a failed push or clear retries on the next reconcile.
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

// decide is the device rule: the tile's toggle, its live gate and a view.
func (t *tile) decide(in *tileInputs) (tileView, bool) {
	if !t.toggle(in) || !t.live(in) {
		return tileView{}, false
	}
	return t.view(in)
}

func isTileApp(name string) bool {
	return slices.ContainsFunc(tiles, func(t tile) bool { return t.app == name })
}

// previewTiles renders the named cards' frames from in, in tiles order: a
// card shows when its toggle is on and its view has something to show. The
// live gate is skipped on purpose: a preview shows what the tile looks like,
// not whether the clock would show it right now.
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
		p.Frames = append(p.Frames, render.CardFrame{Card: t.card, Pixels: render.HexPixels(&v.frame)})
	}
	return p
}

// tileInputs reads the live config and stores for a reconcile at now. A nil
// store reads as no data, which keeps its tiles off the device.
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

// reconcileTiles pushes, refreshes or clears every standalone rotating tile
// and any legacy usage app. Coordinator goroutine only.
func (c *coordinator) reconcileTiles(now time.Time) {
	c.tiles.reconcile(c.tileInputs(now), coordTileWriter{c})
}

// adoptDeviceManagedApps seeds the tile ledger from the device's app loop
// (see tileSet.adopt). Returns false if the loop can't be read, so the caller
// retries on a later tick once the device is reachable. Coordinator goroutine
// only.
func (c *coordinator) adoptDeviceManagedApps() bool {
	names, err := c.publisher.ListApps(c.runCtx())
	if err != nil {
		c.logger.Warn("device app loop read failed; deferring adopt", "err", err)
		return false
	}
	c.tiles.adopt(names, c.loadCfg().AWTRIX.AppName)
	return true
}

// coordTileWriter is the coordinator's tileWriter: pushes take pushApp's
// in-tick retry, clears go straight to the publisher.
type coordTileWriter struct{ c *coordinator }

func (w coordTileWriter) push(app string, payload map[string]any) error {
	return w.c.pushApp(app, payload)
}

func (w coordTileWriter) clear(app string) error {
	return w.c.publisher.ClearApp(w.c.runCtx(), app)
}
