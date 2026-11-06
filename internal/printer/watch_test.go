package printer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeWatch struct {
	mu     sync.Mutex
	starts int
	active int
	seen   func(Found)
}

func (f *fakeWatch) watch(ctx context.Context, seen func(Found)) error {
	f.mu.Lock()
	f.starts++
	f.active++
	f.seen = seen
	f.mu.Unlock()
	<-ctx.Done()
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	return nil
}

func (f *fakeWatch) counts() (starts, active int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.active
}

func (f *fakeWatch) announce(t *testing.T, found Found) {
	t.Helper()
	waitUntil(t, "a running watch", func() bool { _, a := f.counts(); return a == 1 })
	f.mu.Lock()
	seen := f.seen
	f.mu.Unlock()
	seen(found)
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (h *harness) withWatch() *fakeWatch {
	fw := &fakeWatch{}
	h.p.watch = fw.watch
	h.t.Cleanup(h.p.stopWatch)
	return fw
}

func goOffline(t *testing.T, h *harness, s *stubPrinter) {
	t.Helper()
	s.set(func(s *stubPrinter) { s.down = true })
	for range offlineAfter {
		h.advance(h.step())
	}
	if snap := h.snap(); snap.Phase != PhaseOffline || snap.LastError != ErrCodeUnreachable {
		t.Fatalf("phase %s error %q, want offline", snap.Phase, snap.LastError)
	}
}

func TestOfflinePrinterIsPolledWithinASecondOfItsServiceAppearing(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	fw := h.withWatch()
	h.advance(h.step())
	if starts, _ := fw.counts(); starts != 0 {
		t.Fatalf("watch started while online")
	}
	goOffline(t, h, s)
	for range 3 {
		h.advance(h.step())
	}
	if starts, _ := fw.counts(); starts != 1 {
		t.Fatalf("watch starts = %d, want one for the whole offline spell", starts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.p.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitUntil(t, "Run's first step", func() bool { return s.hit(downStatus) >= offlineAfter+4 })
	polls := s.hit("/api/v1/status")
	s.set(func(s *stubPrinter) { s.down = false })
	at := time.Now()
	fw.announce(t, Found{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: s.srv.URL})
	waitUntil(t, "a poll after the announcement", func() bool { return s.hit("/api/v1/status") > polls })
	if d := time.Since(at); d > time.Second {
		t.Fatalf("poll came %v after the announcement, want within 1 s", d)
	}
	waitUntil(t, "the printer online", func() bool { return h.snap().Phase == PhaseIdle })
	waitUntil(t, "the watch to stop once online", func() bool { _, a := fw.counts(); return a == 0 })
}

const downStatus = "down:/api/v1/status"

func TestAnnouncementResetsTheBackoff(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	fw := h.withWatch()
	h.advance(h.step())
	goOffline(t, h, s)
	var d time.Duration
	for range 3 {
		d = h.step()
		h.advance(d)
	}
	if d != Backoff[3] {
		t.Fatalf("delay before the announcement = %v, want %v", d, Backoff[3])
	}
	fw.announce(t, Found{Host: "prusa-core-one.local.", URL: s.srv.URL})
	var got []time.Duration
	for range 5 {
		got = append(got, h.step())
	}
	want := []time.Duration{PollActive, PollActive, Backoff[0], Backoff[1], Backoff[2]}
	if !slices.Equal(got, want) {
		t.Fatalf("delays after an announcement that still fails = %v, want two quick retries then the backoff from the start %v", got, want)
	}
}

func TestOneWakePerOfflineSpell(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	fw := h.withWatch()
	wakes := func() int {
		select {
		case <-h.p.wake:
			return 1
		default:
			return 0
		}
	}
	h.advance(h.step())
	goOffline(t, h, s)
	wakes()
	same := Found{Host: "prusa-core-one.local.", URL: s.srv.URL}
	fw.announce(t, same)
	if wakes() != 1 {
		t.Fatal("the first announcement did not wake the poller")
	}
	for range 3 {
		h.step()
	}
	fw.announce(t, same)
	if n := wakes(); n != 0 || h.p.appeared.Load() {
		t.Fatalf("a second announcement in the same offline spell woke the poller (mDNS up, HTTP down would cycle the backoff)")
	}
	if d := h.step(); d != Backoff[1] {
		t.Fatalf("delay = %v, want the backoff to keep growing (%v)", d, Backoff[1])
	}

	s.set(func(s *stubPrinter) { s.down = false })
	h.advance(h.step())
	if h.snap().Phase != PhaseIdle {
		t.Fatalf("phase %s, want idle", h.snap().Phase)
	}
	goOffline(t, h, s)
	wakes()
	fw.announce(t, same)
	if wakes() != 1 {
		t.Fatal("the next offline spell's announcement did not wake the poller")
	}
}

func TestNoWatchWhileOnlineDisabledOrLockedOut(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	fw := h.withWatch()
	for range 3 {
		h.advance(h.step())
	}
	if starts, _ := fw.counts(); starts != 0 {
		t.Fatalf("watch started while online")
	}

	goOffline(t, h, s)
	waitUntil(t, "a watch while offline", func() bool { _, a := fw.counts(); return a == 1 })
	h.mu.Lock()
	h.cfg.Enabled = false
	h.mu.Unlock()
	h.step()
	waitUntil(t, "the watch to stop when disabled", func() bool { _, a := fw.counts(); return a == 0 })
	h.step()
	if starts, _ := fw.counts(); starts != 1 {
		t.Fatalf("watch restarted while disabled")
	}

	h.mu.Lock()
	h.cfg.Enabled = true
	h.mu.Unlock()
	s.set(func(s *stubPrinter) { s.down, s.password = false, "other" })
	for range 3 {
		h.advance(h.step())
	}
	if snap := h.snap(); snap.LastError != ErrCodeWrongPassword {
		t.Fatalf("last_error %q, want wrong_password", snap.LastError)
	}
	if starts, active := fw.counts(); starts != 1 || active != 0 {
		t.Fatalf("watch starts %d active %d with a wrong password, want none", starts, active)
	}

	s.set(func(s *stubPrinter) { s.down = true })
	h.p.SetCredentials(Credentials{User: DefaultUser, Password: "other"})
	for range 2 {
		h.advance(h.step())
	}
	waitUntil(t, "a watch while offline again", func() bool { s, a := fw.counts(); return s == 2 && a == 1 })
	h.p.SetCredentials(Credentials{User: DefaultUser, Password: ""})
	h.step()
	waitUntil(t, "the watch to stop without a password", func() bool { _, a := fw.counts(); return a == 0 })
	h.step()
	if starts, _ := fw.counts(); starts != 2 {
		t.Fatalf("watch restarted without a password")
	}
}

func TestNotFoundWatchesAndAnyPrinterWakes(t *testing.T) {
	s := newStubPrinter(t)
	var found []Found
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword,
		func(context.Context) ([]Found, error) { return found, nil })
	fw := h.withWatch()
	h.step()
	if snap := h.snap(); snap.LastError != ErrCodeNotFound {
		t.Fatalf("last_error %q", snap.LastError)
	}
	for range 2 {
		h.step()
	}
	found = []Found{{Name: "MK4", Host: "prusa-mk4.local.", URL: s.srv.URL}}
	fw.announce(t, found[0])
	if !h.p.appeared.Load() {
		t.Fatal("an announcement while nothing was ever found did not wake the poller")
	}
	if d := h.step(); d != PollIdle || h.snap().Phase != PhaseIdle {
		t.Fatalf("after the announcement: delay %v phase %s", d, h.snap().Phase)
	}
	waitUntil(t, "the watch to stop once found", func() bool { _, a := fw.counts(); return a == 0 })
}

func TestWatchMatchesTheChosenPrinterOnly(t *testing.T) {
	auto := Config{Enabled: true, URL: URLAuto, Name: "CORE One"}
	manualIP := Config{Enabled: true, URL: "http://192.168.0.50"}
	manualName := Config{Enabled: true, URL: "http://Prusa-Core-One.local:8080"}
	coreOne := Found{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: "http://192.168.0.50:80"}
	moved := Found{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: "http://192.168.0.77:80"}
	mk4 := Found{Name: "Prusa MK4", Host: "prusa-mk4.local.", URL: "http://192.168.0.51:80"}
	coreOneL := Found{Name: "Prusa CORE One L", Host: "prusa-core-one-l.local.", URL: "http://192.168.0.52:80"}
	cases := []struct {
		name     string
		cfg      Config
		identity string
		named    bool
		f        Found
		want     bool
	}{
		{"auto, named printer", auto, "prusa-core-one.local", true, coreOne, true},
		{"auto, named printer at a new address", auto, "prusa-core-one.local", true, moved, true},
		{"auto, another printer", auto, "prusa-core-one.local", true, mk4, false},
		{"auto, a neighbour that also fits the name", auto, "prusa-core-one.local", true, coreOneL, false},
		{"auto, the fallback printer by host", auto, "prusa-mk4.local", false, mk4, true},
		{"auto, the named printer while on a fallback", auto, "prusa-mk4.local", false, coreOne, true},
		{"auto, none chosen yet: any printer", auto, "", false, mk4, true},
		{"manual IP, same address", manualIP, "", false, coreOne, true},
		{"manual IP, another address", manualIP, "", false, moved, false},
		{"manual IP, another printer", manualIP, "", false, mk4, false},
		{"manual host name", manualName, "", false, moved, true},
		{"manual host name, another printer", manualName, "", false, mk4, false},
		{"manual non-.local name, same printer", Config{Enabled: true, URL: "http://prusa-core-one.lan"}, "", false, moved, true},
		{"manual non-.local name, a longer neighbour", Config{Enabled: true, URL: "http://prusa-core-one-l.lan"}, "", false, moved, false},
		{"manual IPv6", Config{Enabled: true, URL: "http://[fe80::1]"}, "", false, coreOne, false},
	}
	for _, tc := range cases {
		tc.cfg.ApplyDefaults()
		if got := watchMatcher(tc.cfg, tc.identity, tc.named)(tc.f); got != tc.want {
			t.Errorf("%s: match = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestOfflineWithoutAnnouncementsPollsOnlyOnTheBackoff(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	fw := h.withWatch()
	h.advance(h.step())
	goOffline(t, h, s)
	before := s.hit(downStatus)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.p.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done
	if got := s.hit(downStatus) - before; got != 1 {
		t.Fatalf("requests in 300 ms offline with a silent watch = %d, want Run's one step", got)
	}
	if starts, _ := fw.counts(); starts != 1 {
		t.Fatalf("watch starts = %d, want 1", starts)
	}
	waitUntil(t, "the watch to stop with Run", func() bool { _, a := fw.counts(); return a == 0 })
}

func TestIdleTemperaturesFireOnChangeOnlyWhenShownWhileOn(t *testing.T) {
	idle := func(nozzle, bed float64) string {
		return fmt.Sprintf(`{"printer":{"state":"IDLE","temp_bed":%.1f,"target_bed":0,"temp_nozzle":%.1f,"target_nozzle":0}}`, bed, nozzle)
	}
	for _, mode := range []string{ShowPrinting, ShowOn} {
		s := newStubPrinter(t)
		cfg := manualConfig(s.srv.URL)
		cfg.Show = mode
		h := newHarness(t, cfg, testPassword, nil)
		changes := 0
		h.p.OnChange = func() { changes++ }
		s.set(func(s *stubPrinter) { s.status = idle(30, 62) })
		h.advance(h.step())
		first := changes
		var got []int
		for _, bed := range []float64{61.6, 61.0, 55.2, 41.0, 39.4, 38.0} {
			s.set(func(s *stubPrinter) { s.status = idle(30, bed) })
			h.advance(h.step())
			got = append(got, changes-first)
		}
		want := []int{0, 0, 1, 2, 2, 2}
		if mode == ShowPrinting {
			want = []int{0, 0, 0, 0, 0, 0}
		}
		if !slices.Equal(got, want) {
			t.Errorf("show=%s: changes after each cooling poll %v, want %v", mode, got, want)
		}
	}
}

func TestSwitchingShowWhileIdleFiresOnChange(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	changes := 0
	h.p.OnChange = func() { changes++ }
	h.advance(h.step())
	for _, mode := range []string{ShowOn, ShowPrinting} {
		before := changes
		h.mu.Lock()
		h.cfg.Show = mode
		h.mu.Unlock()
		h.advance(h.step())
		if changes != before+1 {
			t.Errorf("show=%s while idle fired %d changes, want 1", mode, changes-before)
		}
	}
}

func TestANewTargetGetsItsOwnWake(t *testing.T) {
	a, b := newStubPrinter(t), newStubPrinter(t)
	h := newHarness(t, manualConfig(a.srv.URL), testPassword, nil)
	fw := h.withWatch()
	h.advance(h.step())
	goOffline(t, h, a)
	fw.announce(t, Found{Host: "a.local.", URL: a.srv.URL})
	for range 4 {
		h.step()
	}
	b.set(func(s *stubPrinter) { s.down = true })
	h.mu.Lock()
	h.cfg.URL = b.srv.URL
	h.mu.Unlock()
	for range 2 {
		h.advance(h.step())
	}
	waitUntil(t, "a watch for the new URL", func() bool { s, a := fw.counts(); return s == 2 && a == 1 })
	fw.announce(t, Found{Host: "b.local.", URL: b.srv.URL})
	if !h.p.appeared.Load() {
		t.Fatal("the new printer's announcement was dropped by the old target's wake")
	}
	if d := h.step(); d != PollActive {
		t.Fatalf("delay after the new target's wake = %v, want a quick retry", d)
	}
}

func TestAWakeRetriesQuicklyAtANewDHCPAddress(t *testing.T) {
	oldAddr, newAddr := newStubPrinter(t), newStubPrinter(t)
	current := oldAddr.srv.URL
	browse := func(context.Context) ([]Found, error) {
		return []Found{{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: current}}, nil
	}
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, browse)
	fw := h.withWatch()
	h.advance(h.step())
	goOffline(t, h, oldAddr)
	newAddr.set(func(s *stubPrinter) { s.down = true })
	current = newAddr.srv.URL
	fw.announce(t, Found{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: current})
	if d := h.step(); d != PollActive || h.snap().Printer.URL != newAddr.srv.URL {
		t.Fatalf("delay %v at %s, want a quick retry at the new address", d, h.snap().Printer.URL)
	}
	newAddr.set(func(s *stubPrinter) { s.down = false })
	if d := h.step(); d != PollIdle || h.snap().Phase != PhaseIdle {
		t.Fatalf("delay %v phase %s, want the printer back", d, h.snap().Phase)
	}
}

func TestAFailingWatchAddsNoPolls(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	var mu sync.Mutex
	starts := 0
	h.p.watch = func(context.Context, func(Found)) error {
		mu.Lock()
		starts++
		mu.Unlock()
		return errors.New("no multicast interface")
	}
	h.t.Cleanup(h.p.stopWatch)
	h.advance(h.step())
	goOffline(t, h, s)
	before := s.hit(downStatus)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.p.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done
	if got := s.hit(downStatus) - before; got != 1 {
		t.Fatalf("requests in 300 ms with a failing watch = %d, want Run's one step", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if starts > 2 {
		t.Fatalf("a failing watch was restarted %d times in 300 ms, want at most once per step", starts)
	}
}

func TestReadyTempKeysOnTheShownValueOnly(t *testing.T) {
	cases := []struct {
		bed, nozzle float64
		noz         bool
		deg         int
	}{
		{61, 215, false, 60},
		{38, 215, true, 215},
		{43, 30, false, 45},
		{30, 30, false, 30},
	}
	for _, tc := range cases {
		noz, deg := Snapshot{Bed: tc.bed, Nozzle: tc.nozzle}.ReadyTemp()
		if noz != tc.noz || deg != tc.deg {
			t.Errorf("bed %.0f nozzle %.0f: nozzle=%t %d°, want nozzle=%t %d°", tc.bed, tc.nozzle, noz, deg, tc.noz, tc.deg)
		}
	}

	st := newStubPrinter(t)
	cfg := manualConfig(st.srv.URL)
	cfg.Show = ShowOn
	h := newHarness(t, cfg, testPassword, nil)
	changes := 0
	h.p.OnChange = func() { changes++ }
	idle := func(nozzle, bed float64) string {
		return fmt.Sprintf(`{"printer":{"state":"IDLE","temp_bed":%.1f,"target_bed":0,"temp_nozzle":%.1f,"target_nozzle":0}}`, bed, nozzle)
	}
	st.set(func(s *stubPrinter) { s.status = idle(215, 61) })
	h.advance(h.step())
	first := changes
	for _, noz := range []float64{180, 120, 70} {
		st.set(func(s *stubPrinter) { s.status = idle(noz, 61) })
		h.advance(h.step())
	}
	if changes != first {
		t.Fatalf("a cooling nozzle behind a warm bed fired %d changes, want none", changes-first)
	}
}

func TestAWakeFromNotFoundKeepsBothRetries(t *testing.T) {
	s := newStubPrinter(t)
	var found []Found
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword,
		func(context.Context) ([]Found, error) { return found, nil })
	fw := h.withWatch()
	h.step()
	s.set(func(s *stubPrinter) { s.down = true })
	found = []Found{{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: s.srv.URL}}
	fw.announce(t, found[0])
	var got []time.Duration
	for range 4 {
		got = append(got, h.step())
	}
	want := []time.Duration{PollActive, PollActive, Backoff[0], Backoff[1]}
	if !slices.Equal(got, want) {
		t.Fatalf("delays after a wake from not_found = %v, want %v", got, want)
	}
}

func TestTheListenerIsRefreshedEveryFiveMinutes(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	fw := h.withWatch()
	h.advance(h.step())
	goOffline(t, h, s)
	h.advance(Backoff[0])
	h.step()
	if starts, _ := fw.counts(); starts != 1 {
		t.Fatalf("listener restarted inside the refresh window: %d starts", starts)
	}
	h.advance(WatchRefresh)
	h.step()
	waitUntil(t, "a refreshed listener", func() bool { s, a := fw.counts(); return s == 2 && a == 1 })
}
