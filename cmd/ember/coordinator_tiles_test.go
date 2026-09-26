package main

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
	"github.com/tarakanof/ember/internal/render"
)

// fakeTileWriter is the test adapter at the tileSet seam: it records writes
// and fails the next pushes/clears on demand.
type fakeTileWriter struct {
	failPush, failClear int
	pushedApps, cleared []string
}

func (w *fakeTileWriter) push(app string, _ map[string]any) error {
	if w.failPush > 0 {
		w.failPush--
		return errors.New("lost")
	}
	w.pushedApps = append(w.pushedApps, app)
	return nil
}

func (w *fakeTileWriter) clear(app string) error {
	if w.failClear > 0 {
		w.failClear--
		return errors.New("lost")
	}
	w.cleared = append(w.cleared, app)
	return nil
}

// airOnly is a fresh reading with only the air tile turned on, so a
// reconcile touches exactly one app.
func airOnly(now time.Time, aqi float64) tileInputs {
	var cfg WeatherConfig
	cfg.applyDefaults()
	cfg.Enabled = true
	cfg.RotateInApps, cfg.ForecastTile = boolPtr(false), boolPtr(false)
	return tileInputs{now: now, weather: cfg,
		air: airObservation{AQI: aqi, FetchedAt: now}, haveAir: true}
}

func TestTileSetDedupesAndRefreshes(t *testing.T) {
	var s tileSet
	w := &fakeTileWriter{}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	s.reconcile(airOnly(now, 40), w)
	s.reconcile(airOnly(now.Add(time.Minute), 40), w)
	if got := len(w.pushedApps); got != 1 {
		t.Fatalf("unchanged tile re-pushed: %d pushes, want 1", got)
	}
	s.reconcile(airOnly(now.Add(2*time.Minute), 90), w)
	if got := len(w.pushedApps); got != 2 {
		t.Fatalf("changed tile not re-pushed: %d pushes, want 2", got)
	}
	// Unchanged but due: re-pushed before the device lifetime evicts it.
	s.reconcile(airOnly(now.Add(2*time.Minute+usageRefreshInterval), 90), w)
	if got := len(w.pushedApps); got != 3 {
		t.Fatalf("due tile not refreshed: %d pushes, want 3", got)
	}
}

func TestTileSetFailedWritesRetry(t *testing.T) {
	var s tileSet
	w := &fakeTileWriter{failPush: 1}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	s.reconcile(airOnly(now, 40), w)
	if _, tracked := s.pushed["ember-air"]; tracked {
		t.Fatal("a failed push must not enter the ledger")
	}
	s.reconcile(airOnly(now, 40), w)
	if got := len(w.pushedApps); got != 1 {
		t.Fatalf("failed push not retried: %d successful pushes, want 1", got)
	}

	// Air turned off: the clear fails once, stays tracked, and retries.
	off := airOnly(now, 40)
	off.weather.AirTile = boolPtr(false)
	w.failClear = 1
	s.reconcile(off, w)
	if _, tracked := s.pushed["ember-air"]; !tracked {
		t.Fatal("a failed clear must stay in the ledger to retry")
	}
	s.reconcile(off, w)
	s.reconcile(off, w)
	if !slices.Equal(w.cleared, []string{"ember-air"}) {
		t.Fatalf("cleared = %v, want exactly one ember-air clear", w.cleared)
	}
}

func TestTileSetAdoptAndForget(t *testing.T) {
	var s tileSet
	w := &fakeTileWriter{}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	s.adopt([]string{"Time", "ember", "ember-air", "ember-meet", "ember-usage-codex-5h", "someone-else"}, "ember")
	var got []string
	for name := range s.pushed {
		got = append(got, name)
	}
	slices.Sort(got)
	if want := []string{"ember-air", "ember-meet", "ember-usage-codex-5h"}; !slices.Equal(got, want) {
		t.Fatalf("adopted %v, want %v", got, want)
	}

	// Adopted entries have unknown content: a wanted tile is re-pushed, the
	// rest (legacy usage, meet with no meeting) are cleared.
	s.reconcile(airOnly(now, 40), w)
	if !slices.Equal(w.pushedApps, []string{"ember-air"}) {
		t.Errorf("pushed %v, want [ember-air]", w.pushedApps)
	}
	slices.Sort(w.cleared)
	if want := []string{"ember-meet", "ember-usage-codex-5h"}; !slices.Equal(w.cleared, want) {
		t.Errorf("cleared %v, want %v", w.cleared, want)
	}

	// After a reboot the device holds nothing: forget re-pushes at once.
	s.forget()
	s.reconcile(airOnly(now.Add(time.Second), 40), w)
	if got := len(w.pushedApps); got != 2 {
		t.Errorf("forget did not force a re-push: %d pushes, want 2", got)
	}
}

func TestPreviewTilesHonoursToggleNotLiveGate(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	var cfg WeatherConfig
	cfg.applyDefaults() // feature disabled, stale reading: the device shows nothing
	cfg.ForecastTile = boolPtr(false)
	in := tileInputs{now: now, weather: cfg,
		obs:      weatherObservation{Condition: render.WeatherRain, TempC: 3, Hourly: arc(24), FetchedAt: now.Add(-24 * time.Hour)},
		air:      airObservation{AQI: 20},
		nextMeet: meetings.Occurrence{Title: "1:1", Start: now.Add(3 * time.Hour)},
	}
	for i := range tiles {
		if _, want := tiles[i].decide(&in); want {
			t.Fatalf("fixture: %s would be on the device", tiles[i].app)
		}
	}

	p := previewTiles(in, "weather", "forecast", "air")
	var cards []string
	for _, f := range p.Frames {
		cards = append(cards, f.Card)
	}
	if want := []string{"weather", "air"}; !slices.Equal(cards, want) {
		t.Errorf("cards = %v, want %v (forecast toggled off)", cards, want)
	}
	if p := previewTiles(in, "meeting"); len(p.Frames) != 1 {
		t.Errorf("meeting preview frames = %d, want 1 (outside the lead window still previews)", len(p.Frames))
	}
}
