package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/printer"
	"github.com/tarakanof/ember/internal/render"
)

var printT0 = time.Date(2026, 11, 5, 15, 48, 0, 0, time.UTC)

func printSnap(phase printer.Phase, mut func(*printer.Snapshot)) printer.Snapshot {
	s := printer.Snapshot{Enabled: true, Phase: phase, JobID: 7, Progress: 42,
		EndsAt: printT0.Add(102 * time.Minute), StartedAt: printT0.Add(-134 * time.Minute)}
	if mut != nil {
		mut(&s)
	}
	return s
}

func printInputs(s printer.Snapshot, clockApp bool, icons bool) tileInputs {
	cfg := printer.Config{Enabled: true}
	cfg.ApplyDefaults()
	cfg.ClockApp = boolPtr(clockApp)
	return tileInputs{now: printT0, printCfg: cfg, print: s, printIcons: icons}
}

func pushedPrintApps(t *testing.T, in tileInputs) ([]string, []string) {
	t.Helper()
	var s tileSet
	w := &fakeTileWriter{}
	s.reconcile(in, w)
	var apps []string
	for _, a := range w.pushedApps {
		if isPrintApp(a) {
			apps = append(apps, a)
		}
	}
	return apps, w.cleared
}

func TestPrintAppRotationMembership(t *testing.T) {
	hold := func(s *printer.Snapshot) { s.HoldUntil = printT0.Add(20 * time.Minute) }
	cases := []struct {
		name     string
		snap     printer.Snapshot
		printing bool
		on       bool
	}{
		{"off", printSnap(printer.PhaseOff, nil), false, false},
		{"offline", printSnap(printer.PhaseOffline, nil), false, false},
		{"idle", printSnap(printer.PhaseIdle, nil), false, true},
		{"error", printSnap(printer.PhaseError, nil), false, false},
		{"heating", printSnap(printer.PhaseHeating, nil), true, true},
		{"preparing", printSnap(printer.PhasePreparing, nil), true, true},
		{"printing", printSnap(printer.PhasePrinting, nil), true, true},
		{"finishing", printSnap(printer.PhaseFinishing, nil), true, true},
		{"paused", printSnap(printer.PhasePaused, nil), true, true},
		{"attention", printSnap(printer.PhaseAttention, nil), true, true},
		{"finished inside the hold", printSnap(printer.PhaseFinished, hold), true, true},
		{"finished after the hold", printSnap(printer.PhaseFinished, nil), false, false},
		{"stopped inside the hold", printSnap(printer.PhaseStopped, hold), true, true},
		{"stopped after the hold", printSnap(printer.PhaseStopped, nil), false, false},
		{"disabled mid-print", printSnap(printer.PhasePrinting, func(s *printer.Snapshot) { s.Enabled = false }), false, false},
		{"disabled while idle", printSnap(printer.PhaseIdle, func(s *printer.Snapshot) { s.Enabled = false }), false, false},
	}
	for _, mode := range []string{printer.ShowPrinting, printer.ShowOn, "", "always"} {
		cfg := printer.Config{Enabled: true, Show: mode}
		cfg.ApplyDefaults()
		for _, tc := range cases {
			want := tc.printing
			if mode == printer.ShowOn {
				want = tc.on
			}
			if got := printInRotation(tc.snap, cfg); got != want {
				t.Errorf("show=%q %s: in rotation = %t, want %t", mode, tc.name, got, want)
			}
		}
	}
	covered := map[printer.Phase]bool{}
	for _, tc := range cases {
		covered[tc.snap.Phase] = true
	}
	for _, ph := range []printer.Phase{printer.PhaseOff, printer.PhaseOffline, printer.PhaseIdle, printer.PhaseHeating,
		printer.PhasePreparing, printer.PhasePrinting, printer.PhaseFinishing, printer.PhasePaused, printer.PhaseAttention,
		printer.PhaseFinished, printer.PhaseStopped, printer.PhaseError} {
		if !covered[ph] {
			t.Errorf("phase %s has no rotation case", ph)
		}
	}
}

func showOn(in tileInputs) tileInputs {
	in.printCfg.Show = printer.ShowOn
	return in
}

func TestPrintReadyCardWhileTheIdlePrinterIsOn(t *testing.T) {
	cool := printSnap(printer.PhaseIdle, func(s *printer.Snapshot) { s.Nozzle, s.Bed = 24, 25 })
	apps, _ := pushedPrintApps(t, showOn(printInputs(cool, true, true)))
	if !slices.Equal(apps, []string{printTile.app}) {
		t.Errorf("cool idle printer pushes %v, want the Ready card only", apps)
	}
	warm := printSnap(printer.PhaseIdle, func(s *printer.Snapshot) { s.Nozzle, s.Bed = 38, 61 })
	apps, _ = pushedPrintApps(t, showOn(printInputs(warm, true, true)))
	if !slices.Equal(apps, []string{printTile.app, printTempTile.app}) {
		t.Errorf("warm idle printer pushes %v, want Ready and the temperature card", apps)
	}
	hotNozzle := printSnap(printer.PhaseIdle, func(s *printer.Snapshot) { s.Nozzle, s.Bed = 43, 30 })
	apps, _ = pushedPrintApps(t, showOn(printInputs(hotNozzle, true, true)))
	if !slices.Equal(apps, []string{printTile.app, printTempTile.app}) {
		t.Errorf("warm nozzle pushes %v, want Ready and the temperature card", apps)
	}
	edge := printSnap(printer.PhaseIdle, func(s *printer.Snapshot) { s.Nozzle, s.Bed = 30, 42 })
	apps, _ = pushedPrintApps(t, showOn(printInputs(edge, true, true)))
	if !slices.Equal(apps, []string{printTile.app}) {
		t.Errorf("a bed that would read 40° pushes %v, want Ready only", apps)
	}
	apps, _ = pushedPrintApps(t, showOn(printInputs(warm, false, true)))
	if len(apps) != 0 {
		t.Errorf("clock_app=false still pushes %v", apps)
	}
	in := showOn(printInputs(warm, true, true))
	for _, tl := range []*tile{&printTile, &printTempTile} {
		v, ok := tl.view(&in)
		if !ok {
			t.Fatalf("%s has no view", tl.app)
		}
		if _, icon := v.payload["icon"]; icon {
			t.Errorf("%s names a sweep icon while idle", tl.app)
		}
		if errs := render.CheckNGPayload(v.payload, false); len(errs) > 0 {
			t.Errorf("%s: %v", tl.app, errs)
		}
		want := v.frame()
		if got := render.PrintTileFrame(printViewOf(warm, printT0), map[*tile]int{&printTile: render.PrintCardTime, &printTempTile: render.PrintCardTemp}[tl]); got != want {
			t.Errorf("%s frame differs from the render card", tl.app)
		}
	}
}

func TestPrintReadyCardLeavesWhenThePrinterGoesOff(t *testing.T) {
	var s tileSet
	w := &fakeTileWriter{}
	warm := printSnap(printer.PhaseIdle, func(s *printer.Snapshot) { s.Bed = 70 })
	s.reconcile(showOn(printInputs(warm, true, false)), w)
	s.reconcile(showOn(printInputs(printSnap(printer.PhaseOffline, nil), true, false)), w)
	for _, a := range []string{printTile.app, printTempTile.app} {
		if !slices.Contains(w.cleared, a) {
			t.Errorf("%s still in the rotation offline: cleared %v", a, w.cleared)
		}
	}
}

func TestPrintPowerOnShowsNoPopup(t *testing.T) {
	r := newPrintNoteRig(t, func(c *printer.Config) { c.Show = printer.ShowOn })
	r.snap = printSnap(printer.PhaseOffline, nil)
	r.tick(time.Second)
	r.snap = printSnap(printer.PhaseIdle, func(s *printer.Snapshot) { s.Bed = 60 })
	r.tick(time.Second)
	r.tick(time.Minute)
	if n := r.pub.NotifySnapshot(); len(n) != 0 {
		t.Errorf("power-on sent %d popups, want none", len(n))
	}
}

func TestPrintTilesFollowThePhase(t *testing.T) {
	apps, _ := pushedPrintApps(t, printInputs(printSnap(printer.PhasePrinting, nil), true, false))
	want := []string{printTile.app, printReadyTile.app, printPctTile.app}
	if !slices.Equal(apps, want) {
		t.Errorf("printing pushes %v, want %v", apps, want)
	}
	apps, _ = pushedPrintApps(t, printInputs(printSnap(printer.PhaseHeating, nil), true, false))
	if !slices.Equal(apps, []string{printTile.app}) {
		t.Errorf("heating pushes %v, want one card", apps)
	}
	noEstimate := printSnap(printer.PhasePrinting, func(s *printer.Snapshot) { s.EndsAt = time.Time{} })
	apps, _ = pushedPrintApps(t, printInputs(noEstimate, true, false))
	if !slices.Equal(apps, []string{printTile.app}) {
		t.Errorf("printing without an estimate pushes %v, want the percent card only", apps)
	}
}

func TestIdlePrinterTouchesNoClockApp(t *testing.T) {
	for _, phase := range []printer.Phase{printer.PhaseOff, printer.PhaseOffline, printer.PhaseIdle} {
		var s tileSet
		w := &fakeTileWriter{}
		in := printInputs(printSnap(phase, nil), true, true)
		s.reconcile(in, w)
		s.reconcile(in, w)
		for _, a := range append(w.pushedApps, w.cleared...) {
			if isPrintApp(a) {
				t.Errorf("%s: touched the clock app %s", phase, a)
			}
		}
	}
}

func isPrintApp(a string) bool {
	return slices.ContainsFunc(printTiles, func(tl *tile) bool { return tl.app == a })
}

func TestPrintAppLeavesWhenTheHoldEnds(t *testing.T) {
	var s tileSet
	w := &fakeTileWriter{}
	held := printSnap(printer.PhaseFinished, func(s *printer.Snapshot) { s.HoldUntil = printT0.Add(time.Minute) })
	s.reconcile(printInputs(held, true, false), w)
	if !slices.Contains(w.pushedApps, printTile.app) {
		t.Fatalf("finished inside the hold is not shown: %v", w.pushedApps)
	}
	s.reconcile(printInputs(printSnap(printer.PhaseFinished, nil), true, false), w)
	if !slices.Contains(w.cleared, printTile.app) {
		t.Errorf("app still in the rotation after the hold: cleared %v", w.cleared)
	}
}

func TestPrintClockAppToggleTurnsTheAppOff(t *testing.T) {
	apps, _ := pushedPrintApps(t, printInputs(printSnap(printer.PhasePrinting, nil), false, true))
	if len(apps) != 0 {
		t.Errorf("clock_app=false still pushes %v", apps)
	}
}

func TestPrintPreviewMatchesDevicePayload(t *testing.T) {
	app := NewApp(func() Config { c := defaultConfig(); c.applyDefaults(); return c }(), &recordingPublisher{}, testLogger())
	p := app.printPreview(printT0)
	if len(p.Frames) != 3 {
		t.Fatalf("idle preview shows %d cards, want the sample's 3", len(p.Frames))
	}
	in := printInputs(samplePrintSnapshot(printT0), true, false)
	v := printViewOf(in.print, printT0)
	for card, idx := range map[string]int{"print": render.PrintCardTime, "print-eta": render.PrintCardReady, "print-pct": render.PrintCardPct} {
		f := render.PrintTileFrame(v, idx)
		want := render.HexPixels(&f)
		if got := previewCard(t, p, card); !slices.Equal(got, want) {
			t.Errorf("card %s differs from the tile frame", card)
		}
	}
}

type printNoteRig struct {
	c     *coordinator
	pub   *recordingPublisher
	snap  printer.Snapshot
	now   time.Time
	quiet bool
}

func newPrintNoteRig(t *testing.T, mut func(*printer.Config)) *printNoteRig {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Printer.Enabled = true
	cfg.Printer.ApplyDefaults()
	if mut != nil {
		mut(&cfg.Printer)
	}
	pub := &recordingPublisher{}
	clk := &fakeClock{now: printT0}
	c := newCoordinator(cfg, nil, pub, clk, testLogger(), nil)
	r := &printNoteRig{c: c, pub: pub, now: printT0}
	c.printSnap = func(time.Time) printer.Snapshot { return r.snap }
	c.quiet = func(time.Time) bool { return r.quiet }
	return r
}

func (r *printNoteRig) tick(d time.Duration) {
	r.now = r.now.Add(d)
	r.c.checkPrint(r.now)
}

func (r *printNoteRig) event(kind printer.EventKind, id uint64, at time.Time) {
	r.snap.Events = append(r.snap.Events, printer.Event{ID: id, Kind: kind, At: at, JobID: 7})
}

func TestPrintDonePopupFiresOncePerJob(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.snap = printSnap(printer.PhaseFinished, nil)
	r.event(printer.EventFinished, 100, printT0)
	for range 5 {
		r.tick(3 * time.Second)
	}
	n := r.pub.NotifySnapshot()
	if len(n) != 1 {
		t.Fatalf("%d popups, want 1", len(n))
	}
	if n[0]["text"] != "DONE 2:14" || n[0]["name"] != notifyNamePrintDone || n[0]["soundRtttl"] != printDoneRTTTL {
		t.Errorf("popup = %v", n[0])
	}
	if n[0]["durationMs"] != 10_000 {
		t.Errorf("duration = %v, want 10 s", n[0]["durationMs"])
	}
}

func TestPrintDonePopupAfterARestart(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.snap = printSnap(printer.PhaseFinished, nil)
	for range 3 {
		r.tick(3 * time.Second)
	}
	if n := r.pub.NotifySnapshot(); len(n) != 0 {
		t.Errorf("a restart that reads FINISHED replayed %d popups", len(n))
	}
}

func TestPrintQuietHoursShowTheDonePopupWithoutSound(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.quiet = true
	r.snap = printSnap(printer.PhaseFinished, nil)
	r.event(printer.EventFinished, 100, printT0)
	r.tick(time.Second)
	n := r.pub.NotifySnapshot()
	if len(n) != 1 {
		t.Fatalf("%d popups, want 1", len(n))
	}
	for _, k := range []string{"sound", "soundRtttl", "soundLoop"} {
		if _, ok := n[0][k]; ok {
			t.Errorf("quiet-hours popup carries %s", k)
		}
	}
}

func TestPrintTogglesAndStaleEventsStaySilent(t *testing.T) {
	r := newPrintNoteRig(t, func(c *printer.Config) { c.NotifyDone, c.NotifyAttention = boolPtr(false), boolPtr(false) })
	r.snap = printSnap(printer.PhaseAttention, nil)
	r.event(printer.EventFinished, 100, printT0)
	r.event(printer.EventAttention, 101, printT0)
	r.event(printer.EventError, 102, printT0)
	r.tick(time.Second)
	if n := r.pub.NotifySnapshot(); len(n) != 0 {
		t.Errorf("toggles off still popped %d", len(n))
	}
	late := newPrintNoteRig(t, nil)
	late.snap = printSnap(printer.PhaseFinished, nil)
	late.event(printer.EventFinished, 100, printT0.Add(-20*time.Minute))
	late.tick(time.Second)
	if n := late.pub.NotifySnapshot(); len(n) != 0 {
		t.Errorf("a 20-minute-old finish popped")
	}
}

func TestPrintDonePopupRetriesAfterAFailedPush(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	fail := true
	r.pub.failNotify = func() error {
		if fail {
			return errors.New("clock down")
		}
		return nil
	}
	r.snap = printSnap(printer.PhaseFinished, nil)
	r.event(printer.EventFinished, 100, printT0)
	r.tick(3 * time.Second)
	fail = false
	r.tick(3 * time.Second)
	r.tick(printRetryAfter)
	if n := r.pub.NotifySnapshot(); len(n) != 1 {
		t.Errorf("%d popups after the retry, want 1", len(n))
	}
}

func TestPrintAttentionRepeatsThenDismisses(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.snap = printSnap(printer.PhaseAttention, nil)
	r.event(printer.EventAttention, 100, printT0)
	r.tick(time.Second)
	n := r.pub.NotifySnapshot()
	if len(n) != 1 || n[0]["name"] != notifyNamePrintAttention || n[0]["durationMs"] != 120_000 || n[0]["textColor"] != "#FFC14D" {
		t.Fatalf("first attention popup = %v", n)
	}
	r.tick(9 * time.Minute)
	if len(r.pub.NotifySnapshot()) != 1 {
		t.Errorf("repeated before 10 minutes")
	}
	r.tick(time.Minute)
	if len(r.pub.NotifySnapshot()) != 2 {
		t.Errorf("did not repeat after 10 minutes")
	}
	r.snap = printSnap(printer.PhasePrinting, nil)
	r.tick(3 * time.Second)
	if d := r.pub.DismissedNamesSnapshot(); !slices.Equal(d, []string{notifyNamePrintAttention}) {
		t.Errorf("dismissed %v when attention cleared", d)
	}
	r.tick(20 * time.Minute)
	if len(r.pub.NotifySnapshot()) != 2 {
		t.Errorf("popped after attention cleared")
	}
}

func TestPrintErrorPopupIsRed(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.snap = printSnap(printer.PhaseError, nil)
	r.event(printer.EventError, 100, printT0)
	r.tick(time.Second)
	n := r.pub.NotifySnapshot()
	if len(n) != 1 || n[0]["name"] != notifyNamePrintError || n[0]["textColor"] != "#FF5A3A" {
		t.Errorf("error popup = %v", n)
	}
}

func TestPrintStartedAndStoppedDoNotPop(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.snap = printSnap(printer.PhaseStopped, nil)
	r.event(printer.EventStarted, 100, printT0)
	r.event(printer.EventStopped, 101, printT0)
	r.tick(time.Second)
	if n := r.pub.NotifySnapshot(); len(n) != 0 {
		t.Errorf("popped %d for started/stopped", len(n))
	}
}

func printIconApp(t *testing.T, enabled, clockApp bool, icons []string) (*App, *recordingPublisher) {
	t.Helper()
	pub := &recordingPublisher{icons: append(waitIconFiles(), icons...)}
	cfg := defaultConfig()
	cfg.Weather.Enabled = false
	cfg.Printer.Enabled = enabled
	cfg.Printer.ApplyDefaults()
	cfg.Printer.ClockApp = boolPtr(clockApp)
	app := NewApp(cfg, pub, testLogger())
	ownIconJobs(t, app)
	return app, pub
}

func TestPrintSweepIconsAreProvisionedAndArmed(t *testing.T) {
	app, pub := printIconApp(t, true, true, []string{render.PrintIconPrefix + "h0_deadbeef.gif"})
	app.ensureNativeIcons(context.Background())
	got := pub.PutIconNamesSnapshot()
	icons, _ := render.PrintIcons()
	if len(got) != len(icons) {
		t.Fatalf("uploaded %v, want %d print icons", got, len(icons))
	}
	for _, f := range got {
		if !strings.HasPrefix(f, render.PrintIconPrefix) || !strings.HasSuffix(f, ".gif") {
			t.Errorf("unexpected upload %s", f)
		}
	}
	if !app.printIcons.Load() {
		t.Error("icons uploaded but not armed")
	}
	if d := pub.DeletedIconsSnapshot(); !slices.Equal(d, []string{render.PrintIconPrefix + "h0_deadbeef.gif"}) {
		t.Errorf("stale print icon deletion = %v", d)
	}
}

func TestPrintSweepIconsStayAwayWhenTheAppIsOff(t *testing.T) {
	for _, tc := range []struct{ enabled, clockApp bool }{{false, true}, {true, false}} {
		app, pub := printIconApp(t, tc.enabled, tc.clockApp, nil)
		app.ensureNativeIcons(context.Background())
		if got := pub.PutIconNamesSnapshot(); len(got) != 0 {
			t.Errorf("enabled=%t clock_app=%t uploaded %v", tc.enabled, tc.clockApp, got)
		}
		if app.printIcons.Load() {
			t.Errorf("enabled=%t clock_app=%t armed icons it does not hold", tc.enabled, tc.clockApp)
		}
	}
}

func TestPrintAttentionDismissRetriesUntilItLands(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.snap = printSnap(printer.PhaseAttention, nil)
	r.event(printer.EventAttention, 100, printT0)
	r.tick(time.Second)
	r.pub.dismissByNameErr = errors.New("clock down")
	r.snap = printSnap(printer.PhasePrinting, nil)
	r.tick(3 * time.Second)
	r.tick(3 * time.Second)
	r.tick(printRetryAfter)
	r.pub.dismissByNameErr = nil
	r.tick(printRetryAfter)
	r.tick(printRetryAfter)
	if d := r.pub.DismissedNamesSnapshot(); len(d) != 3 {
		t.Errorf("dismiss attempts = %d, want 3 (two failed, one landed, then stop)", len(d))
	}
}

func TestPrintAttentionSurvivesAShortOfflineGap(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.snap = printSnap(printer.PhaseAttention, nil)
	r.event(printer.EventAttention, 100, printT0)
	r.tick(time.Second)
	r.snap = printSnap(printer.PhaseOffline, nil)
	r.tick(3 * time.Second)
	if d := r.pub.DismissedNamesSnapshot(); len(d) != 0 {
		t.Fatalf("dismissed %v while the printer was only offline", d)
	}
	r.snap = printSnap(printer.PhaseAttention, nil)
	r.tick(printAttentionRepeat)
	if n := r.pub.NotifySnapshot(); len(n) != 2 {
		t.Errorf("%d attention popups, want the repeat after the gap", len(n))
	}
}

func TestPrintPreviewShowsOnlyTheCardsTheClockRotates(t *testing.T) {
	app := NewApp(func() Config { c := defaultConfig(); c.applyDefaults(); return c }(), &recordingPublisher{}, testLogger())
	in := tileInputs{now: printT0, printCfg: app.cfg.Load().Printer, print: printSnap(printer.PhasePaused, nil)}
	var cards []string
	for _, tl := range printTiles {
		if tl.live(&in) {
			cards = append(cards, tl.card)
		}
	}
	if !slices.Equal(cards, []string{"print"}) {
		t.Errorf("paused shows cards %v, want one", cards)
	}
}

func TestPrintTilePayloadsFitNGSchema(t *testing.T) {
	for _, icons := range []bool{false, true} {
		for _, phase := range []printer.Phase{printer.PhaseHeating, printer.PhasePrinting, printer.PhaseAttention, printer.PhaseFinished} {
			in := printInputs(printSnap(phase, nil), true, icons)
			for _, tl := range printTiles {
				v, ok := tl.view(&in)
				if !ok {
					continue
				}
				if errs := render.CheckNGPayload(v.payload, false); len(errs) > 0 {
					t.Errorf("%s %s icons=%t: %v", tl.app, phase, icons, errs)
				}
			}
		}
	}
}

func TestPrinterChangeRunsPrintWorkWithoutAdvancingTheCards(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.c.snapshot = func() Snapshot { return Snapshot{} }
	r.c.cardCursor = 4
	r.snap = printSnap(printer.PhaseFinished, nil)
	r.event(printer.EventFinished, 100, printT0)
	r.c.handle(coordCmd{kind: cmdPrint})
	if len(r.pub.NotifySnapshot()) != 1 {
		t.Errorf("a printer change did not run the popup check")
	}
	if r.c.cardCursor != 4 {
		t.Errorf("cardCursor moved to %d on a printer change", r.c.cardCursor)
	}
}

func TestPrintHeatingTemperaturesMoveInSteps(t *testing.T) {
	a := printViewOf(printSnap(printer.PhaseHeating, func(s *printer.Snapshot) { s.Nozzle, s.Bed = 151, 41 }), printT0)
	b := printViewOf(printSnap(printer.PhaseHeating, func(s *printer.Snapshot) { s.Nozzle, s.Bed = 152, 42 }), printT0)
	if a.Nozzle != b.Nozzle || a.Bed != b.Bed {
		t.Errorf("a 2° wobble changes the view: %+v vs %+v", a, b)
	}
}

func TestPrintFailedPopupWaitsBeforeRetrying(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	calls := 0
	r.pub.failNotify = func() error { calls++; return errors.New("down") }
	r.snap = printSnap(printer.PhaseFinished, nil)
	r.event(printer.EventFinished, 100, printT0)
	for range 5 {
		r.tick(3 * time.Second)
	}
	if calls != 1 {
		t.Errorf("%d attempts in 15 s, want 1", calls)
	}
	r.tick(30 * time.Second)
	if calls != 2 {
		t.Errorf("no retry after the wait: %d attempts", calls)
	}
}

func TestPrintAttentionStillRepeatsWhenTheFirstPopupWasMissed(t *testing.T) {
	r := newPrintNoteRig(t, nil)
	r.snap = printSnap(printer.PhaseAttention, nil)
	r.event(printer.EventAttention, 100, printT0.Add(-10*time.Minute))
	r.tick(time.Second)
	if n := r.pub.NotifySnapshot(); len(n) != 1 || n[0]["name"] != notifyNamePrintAttention {
		t.Errorf("a prompt that outlived the missed first popup got %v", n)
	}
}

func TestPrintDonePopupNamesTheBurstIconWhenProvisioned(t *testing.T) {
	for _, armed := range []bool{true, false} {
		r := newPrintNoteRig(t, nil)
		r.c.printIcons = func() bool { return armed }
		r.snap = printSnap(printer.PhaseFinished, nil)
		r.event(printer.EventFinished, 100, printT0)
		r.tick(3 * time.Second)
		n := r.pub.NotifySnapshot()
		if len(n) != 1 {
			t.Fatalf("armed=%v: %d popups, want 1", armed, len(n))
		}
		icon, _ := n[0]["icon"].(string)
		if armed {
			if icon != render.PrintDoneIconName() {
				t.Errorf("icon = %q, want the burst icon", icon)
			}
			if n[0]["draw"] != nil || n[0]["textOffsetX"] != nil {
				t.Errorf("armed popup keeps the bitmap or text offset: %v", n[0])
			}
			if n[0]["text"] != "DONE 2:14" {
				t.Errorf("text = %v", n[0]["text"])
			}
		} else if icon != "" || n[0]["draw"] == nil {
			t.Errorf("unarmed popup = %v, want the static bitmap", n[0])
		}
	}
}

func TestPrintBurstIconIsProvisionedWithTheSweeps(t *testing.T) {
	app, pub := printIconApp(t, true, true, nil)
	app.ensureNativeIcons(context.Background())
	want := render.PrintDoneIconName() + ".gif"
	if !slices.Contains(pub.PutIconNamesSnapshot(), want) {
		t.Errorf("uploaded %v, missing %s", pub.PutIconNamesSnapshot(), want)
	}
}

func TestPrintPreviewShowsTheReadyCardsWhileTheIdlePrinterIsShown(t *testing.T) {
	warm := printSnap(printer.PhaseIdle, func(s *printer.Snapshot) { s.Bed, s.Nozzle = 64, 30 })
	in := showOn(printInputs(warm, true, false))
	p := printPreviewOf(in.printCfg, warm, printT0)
	var cards []string
	for _, f := range p.Frames {
		cards = append(cards, f.Card)
	}
	if !slices.Equal(cards, []string{printTile.card, printTempTile.card}) {
		t.Fatalf("idle preview while on shows %v, want the Ready and temperature cards", cards)
	}
	v := printViewOf(warm, printT0)
	for card, idx := range map[string]int{printTile.card: render.PrintCardTime, printTempTile.card: render.PrintCardTemp} {
		f := render.PrintTileFrame(v, idx)
		if got := previewCard(t, p, card); !slices.Equal(got, render.HexPixels(&f)) {
			t.Errorf("card %s differs from the tile frame", card)
		}
	}
	printing := printInputs(warm, true, false)
	if p := printPreviewOf(printing.printCfg, warm, printT0); len(p.Frames) != 3 {
		t.Errorf("idle preview in printing mode shows %d cards, want the printing sample's 3", len(p.Frames))
	}
}
