package printer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func eventKinds(s Snapshot) []EventKind {
	var out []EventKind
	for _, e := range s.Events {
		out = append(out, e.Kind)
	}
	return out
}

func TestRecordedPrintYieldsPhasesAndOneEventPerEdge(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	display := "bracket_v3_0.4n_0.2mm_PETG_COREONE_2h14m.bgcode"
	steps := []struct {
		status string
		job    string
		want   Phase
	}{
		{statusJSON("IDLE", nil), "", PhaseIdle},
		{statusJSON("PRINTING", &jobFields{id: 412, progress: 0, nozzle: 120, nozzleT: 240, bed: 60, bedT: 85}), jobJSON(412, "PRINTING", display), PhaseHeating},
		{statusJSON("PRINTING", &jobFields{id: 412, progress: 0, nozzle: 238, nozzleT: 240, bed: 84, bedT: 85}), jobJSON(412, "PRINTING", display), PhasePreparing},
		{statusJSON("PRINTING", &jobFields{id: 412, progress: 42, remaining: num(4680), printing: num(3360), nozzle: 240, nozzleT: 240, bed: 85, bedT: 85}), jobJSON(412, "PRINTING", display), PhasePrinting},
		{statusJSON("ATTENTION", &jobFields{id: 412, progress: 55, remaining: num(3000), printing: num(4000)}), jobJSON(412, "PAUSED", display), PhaseAttention},
		{statusJSON("ATTENTION", &jobFields{id: 412, progress: 55, remaining: num(3000), printing: num(4000)}), jobJSON(412, "PAUSED", display), PhaseAttention},
		{statusJSON("PRINTING", &jobFields{id: 412, progress: 56, remaining: num(2900), printing: num(4100)}), jobJSON(412, "PRINTING", display), PhasePrinting},
		{statusJSON("PRINTING", &jobFields{id: 412, progress: 100, remaining: num(0), printing: num(8000)}), jobJSON(412, "PRINTING", display), PhaseFinishing},
		{statusJSON("PRINTING", &jobFields{id: 412, progress: 100, remaining: num(0), printing: num(8010)}), jobJSON(412, "PRINTING", display), PhaseFinishing},
		{statusJSON("FINISHED", nil), jobJSON(412, "FINISHED", display), PhaseFinished},
		{statusJSON("FINISHED", nil), jobJSON(412, "FINISHED", display), PhaseFinished},
	}
	var got []Phase
	for _, st := range steps {
		s.set(func(s *stubPrinter) { s.status, s.job = st.status, st.job })
		h.advance(h.step())
		got = append(got, h.snap().Phase)
	}
	var want []Phase
	for _, st := range steps {
		want = append(want, st.want)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("phases = %v\nwant     %v", got, want)
	}
	snap := h.snap()
	if kinds := eventKinds(snap); !slices.Equal(kinds, []EventKind{EventStarted, EventAttention, EventFinished}) {
		t.Fatalf("events = %v", kinds)
	}
	for i := 1; i < len(snap.Events); i++ {
		if snap.Events[i].ID <= snap.Events[i-1].ID {
			t.Fatalf("event ids not increasing: %+v", snap.Events)
		}
	}
	if snap.Name != "bracket_v3" || snap.Material != "PETG" || snap.Progress != 100 || snap.JobID != 412 {
		t.Fatalf("finished snapshot = %+v", snap)
	}
	if snap.Printer.Firmware != "8.0.0+12345" || snap.Printer.Hostname != "prusa-core-one" {
		t.Fatalf("printer info = %+v", snap.Printer)
	}

	restarted := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	restarted.now = h.now
	for range 3 {
		restarted.advance(restarted.step())
	}
	rs := restarted.snap()
	if rs.Phase != PhaseFinished || len(rs.Events) != 0 {
		t.Fatalf("restart mid-FINISHED: phase %s events %v", rs.Phase, eventKinds(rs))
	}
	if !rs.HoldUntil.IsZero() {
		t.Fatal("restart mid-FINISHED holds the done card")
	}
}

func TestRestartMidPrintReplaysNoStarted(t *testing.T) {
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) {
		s.status = statusJSON("PAUSED", &jobFields{id: 9, progress: 30})
		s.job = jobJSON(9, "PAUSED", "cube.gcode")
	})
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	h.advance(h.step())
	s.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 9, progress: 31}) })
	h.advance(h.step())
	if ev := eventKinds(h.snap()); len(ev) != 0 {
		t.Fatalf("events after restart = %v", ev)
	}
	s.set(func(s *stubPrinter) {
		s.status = statusJSON("PRINTING", &jobFields{id: 10, progress: 2})
		s.job = jobJSON(10, "PRINTING", "next.gcode")
	})
	h.advance(h.step())
	if ev := eventKinds(h.snap()); !slices.Equal(ev, []EventKind{EventStarted}) {
		t.Fatalf("events after a new job = %v", ev)
	}
}

func TestStoppedAndErrorEdges(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	for _, st := range []string{
		statusJSON("PRINTING", &jobFields{id: 1, progress: 10}),
		statusJSON("STOPPED", nil),
		statusJSON("STOPPED", nil),
		statusJSON("IDLE", nil),
		statusJSON("PRINTING", &jobFields{id: 2, progress: 10}),
		statusJSON("ERROR", nil),
	} {
		s.set(func(s *stubPrinter) { s.status = st })
		h.advance(h.step())
	}
	if ev := eventKinds(h.snap()); !slices.Equal(ev, []EventKind{EventStopped, EventStarted, EventError}) {
		t.Fatalf("events = %v", ev)
	}
}

func TestEndsAtStaysPutWhileEstimateWobbles(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	t0 := h.now
	poll := func(remaining, printing float64) Snapshot {
		s.set(func(s *stubPrinter) {
			s.status = statusJSON("PRINTING", &jobFields{id: 5, progress: 40, remaining: num(remaining), printing: num(printing)})
		})
		if d := h.step(); d != PollActive {
			t.Fatalf("active poll interval = %v, want %v", d, PollActive)
		}
		snap := h.snap()
		h.advance(PollActive)
		return snap
	}
	first := poll(4680, 3360)
	if want := t0.Add(4680 * time.Second); !first.EndsAt.Equal(want) {
		t.Fatalf("ends_at = %v, want %v", first.EndsAt, want)
	}
	if want := t0.Add(-3360 * time.Second); !first.StartedAt.Equal(want) {
		t.Fatalf("started_at = %v, want %v", first.StartedAt, want)
	}
	for i, rem := range []float64{4700, 4640, 4720, 4615} {
		snap := poll(rem, 3360+float64(5*(i+1))+20)
		if !snap.EndsAt.Equal(first.EndsAt) {
			t.Fatalf("poll %d (remaining %v): ends_at moved to %v", i, rem, snap.EndsAt)
		}
		if !snap.StartedAt.Equal(first.StartedAt) {
			t.Fatalf("poll %d: started_at moved to %v", i, snap.StartedAt)
		}
	}
	moved := poll(4500, 3400)
	if want := h.now.Add(-PollActive).Add(4500 * time.Second); !moved.EndsAt.Equal(want) {
		t.Fatalf("ends_at after a 2-minute shift = %v, want %v", moved.EndsAt, want)
	}
}

func TestPrinterOffGoesOfflineAndBacksOff(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	if d := h.step(); d != PollIdle {
		t.Fatalf("idle poll interval = %v", d)
	}
	h.advance(PollIdle)
	s.srv.Close()

	var delays []time.Duration
	var phases []Phase
	for range 6 {
		d := h.step()
		delays = append(delays, d)
		phases = append(phases, h.snap().Phase)
		h.advance(d)
	}
	wantDelays := []time.Duration{PollIdle, 30 * time.Second, 60 * time.Second, 120 * time.Second, 300 * time.Second, 300 * time.Second}
	if !slices.Equal(delays, wantDelays) {
		t.Fatalf("poll delays = %v, want %v", delays, wantDelays)
	}
	if phases[0] != PhaseIdle || phases[1] != PhaseOffline {
		t.Fatalf("phases = %v, want offline after the second failed poll", phases)
	}
	if got := h.snap().LastError; got != ErrCodeUnreachable {
		t.Fatalf("last_error = %q", got)
	}
	var warns []string
	for _, l := range h.logs.lines() {
		if strings.Contains(l, "level=WARN") {
			warns = append(warns, l)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("warn lines = %d, want 1:\n%s", len(warns), strings.Join(warns, "\n"))
	}
}

func TestWrongPasswordStopsPollingUntilCredentialsChange(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), "not-the-password", nil)
	h.step()
	snap := h.snap()
	if snap.LastError != ErrCodeWrongPassword || snap.Phase != PhaseOffline {
		t.Fatalf("snapshot = phase %s error %q", snap.Phase, snap.LastError)
	}
	before := s.total()
	for range 5 {
		h.advance(h.step())
	}
	if got := s.total(); got != before {
		t.Fatalf("polled the API %d more times after a wrong password", got-before)
	}
	h.p.SetCredentials(Credentials{User: DefaultUser, Password: testPassword})
	h.step()
	if snap := h.snap(); snap.LastError != ErrCodeNone || snap.Phase != PhaseIdle {
		t.Fatalf("after the fix: phase %s error %q", snap.Phase, snap.LastError)
	}
}

func TestWrongPasswordRetriesAfterConfigChange(t *testing.T) {
	s := newStubPrinter(t)
	other := newStubPrinter(t)
	other.set(func(s *stubPrinter) { s.password = "other-password" })
	h := newHarness(t, manualConfig(other.srv.URL), testPassword, nil)
	h.step()
	if h.snap().LastError != ErrCodeWrongPassword {
		t.Fatal("want wrong_password")
	}
	h.mu.Lock()
	h.cfg = manualConfig(s.srv.URL)
	h.mu.Unlock()
	h.step()
	if snap := h.snap(); snap.LastError != ErrCodeNone || snap.Phase != PhaseIdle {
		t.Fatalf("after the URL change: phase %s error %q", snap.Phase, snap.LastError)
	}
}

func TestDisabledAndNoPasswordAreOff(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, Config{URL: s.srv.URL}, testPassword, nil)
	h.step()
	if snap := h.snap(); snap.Phase != PhaseOff || snap.LastError != ErrCodeNone || snap.Enabled {
		t.Fatalf("disabled: %+v", snap)
	}
	h2 := newHarness(t, manualConfig(s.srv.URL), "", nil)
	h2.step()
	if snap := h2.snap(); snap.Phase != PhaseOff || snap.LastError != ErrCodeNoPassword {
		t.Fatalf("no password: %+v", snap)
	}
	if s.total() != 0 {
		t.Fatalf("off printer was polled %d times", s.total())
	}
}

func TestAutoModeNotFound(t *testing.T) {
	h := newHarness(t, Config{Enabled: true, URL: URLAuto}, testPassword, func(context.Context) ([]Found, error) { return nil, nil })
	d := h.step()
	if snap := h.snap(); snap.Phase != PhaseOffline || snap.LastError != ErrCodeNotFound {
		t.Fatalf("snapshot = phase %s error %q", snap.Phase, snap.LastError)
	}
	if d != Backoff[0] {
		t.Fatalf("delay = %v", d)
	}
}

func TestAutoModeRediscoversANewAddressWhenOffline(t *testing.T) {
	oldAddr := newStubPrinter(t)
	newAddr := newStubPrinter(t)
	browses := 0
	current := oldAddr.srv.URL
	browse := func(context.Context) ([]Found, error) {
		browses++
		return []Found{{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: current}}, nil
	}
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, browse)
	h.advance(h.step())
	if snap := h.snap(); snap.Printer.URL != oldAddr.srv.URL || snap.Printer.Host != "prusa-core-one.local" || !snap.Printer.Discovered {
		t.Fatalf("printer = %+v", snap.Printer)
	}
	oldAddr.srv.Close()
	current = newAddr.srv.URL
	elapsed := time.Duration(0)
	for range 3 {
		d := h.step()
		if h.snap().Printer.URL == newAddr.srv.URL && h.snap().Phase == PhaseIdle {
			break
		}
		elapsed += d
		h.advance(d)
	}
	snap := h.snap()
	if snap.Printer.URL != newAddr.srv.URL || snap.Phase != PhaseIdle {
		t.Fatalf("after DHCP move: url %s phase %s", snap.Printer.URL, snap.Phase)
	}
	if elapsed > time.Minute {
		t.Fatalf("recovered after %v, want within a minute", elapsed)
	}
	if browses != 2 {
		t.Fatalf("browses = %d, want 2 (start, turning offline)", browses)
	}
}

func TestManualURLIsNeverRediscovered(t *testing.T) {
	s := newStubPrinter(t)
	browses := 0
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, func(context.Context) ([]Found, error) {
		browses++
		return []Found{{Host: "x.local.", URL: "http://127.0.0.1:9"}}, nil
	})
	h.step()
	s.srv.Close()
	for range 4 {
		h.advance(h.step())
	}
	if browses != 0 {
		t.Fatalf("manual URL browsed %d times", browses)
	}
	if got := h.snap().Printer; got.URL != s.srv.URL || got.Discovered {
		t.Fatalf("printer = %+v", got)
	}
}

func TestChoosePrefersTheConfiguredName(t *testing.T) {
	found := []Found{
		{Name: "Prusa MK4", Host: "prusa-mk4.local.", URL: "http://10.0.0.3"},
		{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: "http://10.0.0.2"},
		{Name: "Workshop", Host: "core-one.local.", URL: "http://10.0.0.4"},
	}
	cases := []struct {
		name, want string
		matched    bool
	}{
		{"CORE One", "http://10.0.0.4", true},
		{"Prusa CORE One", "http://10.0.0.2", true},
		{"MK4", "http://10.0.0.3", true},
		{"XL", "http://10.0.0.4", false},
	}
	for _, c := range cases {
		if got, matched := Choose(found, c.name); got.URL != c.want || matched != c.matched {
			t.Errorf("Choose(%q) = %s %v, want %s %v", c.name, got.URL, matched, c.want, c.matched)
		}
	}
	coreOnes := []Found{
		{Host: "prusa-core-one-l.local.", URL: "http://10.0.0.5"},
		{Host: "prusa-core-one.local.", URL: "http://10.0.0.6"},
	}
	if got, _ := Choose(coreOnes, "CORE One"); got.URL != "http://10.0.0.6" {
		t.Errorf("Choose(CORE One) picked %s, want the CORE One, not the CORE One L", got.URL)
	}
}

type lan struct {
	mu    sync.Mutex
	found []Found
	n     int
}

func (l *lan) set(f ...Found) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.found = f
}

func (l *lan) browse(context.Context) ([]Found, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	return slices.Clone(l.found), nil
}

func TestAutoModeNeverSwitchesToAnotherPrinterWhenTheNamedOneIsOff(t *testing.T) {
	core := newStubPrinter(t)
	mk4 := newStubPrinter(t)
	mk4.set(func(s *stubPrinter) { s.status = statusJSON("FINISHED", nil) })
	net := &lan{}
	net.set(Found{Host: "prusa-core-one.local.", URL: core.srv.URL}, Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	core.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 7, progress: 50}) })
	h.advance(h.step())
	core.srv.Close()
	net.set(Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL})
	for range 5 {
		h.advance(h.step())
	}
	snap := h.snap()
	if snap.Printer.URL != core.srv.URL || snap.Phase != PhaseOffline {
		t.Fatalf("switched to %s (%s), want to stay offline on the CORE One", snap.Printer.URL, snap.Phase)
	}
	if len(snap.Events) != 0 || mk4.total() != 0 {
		t.Fatalf("events %v, MK4 requests %d", eventKinds(snap), mk4.total())
	}
}

func TestAutoModeFallbackMovesToTheNamedPrinterWhenItAppears(t *testing.T) {
	core := newStubPrinter(t)
	mk4 := newStubPrinter(t)
	mk4.set(func(s *stubPrinter) { s.status = statusJSON("FINISHED", nil) })
	net := &lan{}
	net.set(Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	h.advance(h.step())
	if got := h.snap().Printer.URL; got != mk4.srv.URL {
		t.Fatalf("fallback = %s, want the only printer", got)
	}
	core.set(func(s *stubPrinter) { s.status = statusJSON("FINISHED", nil) })
	net.set(Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL}, Found{Host: "prusa-core-one.local.", URL: core.srv.URL})
	h.advance(h.step())
	if got := h.snap().Printer.URL; got != mk4.srv.URL {
		t.Fatalf("browsed again before %v: now on %s", FallbackBrowse, got)
	}
	h.advance(FallbackBrowse)
	h.advance(h.step())
	snap := h.snap()
	if snap.Printer.URL != core.srv.URL || snap.Printer.Host != "prusa-core-one.local" {
		t.Fatalf("printer = %+v, want the CORE One once it appears", snap.Printer)
	}
	if len(snap.Events) != 0 {
		t.Fatalf("switching printers fired %v", eventKinds(snap))
	}
	browsed := net.n
	h.advance(h.step())
	if net.n != browsed {
		t.Fatal("kept browsing after the named printer was found")
	}
}

func TestNameChangeSelectsAnotherPrinter(t *testing.T) {
	core := newStubPrinter(t)
	mk4 := newStubPrinter(t)
	net := &lan{}
	net.set(Found{Host: "prusa-core-one.local.", URL: core.srv.URL}, Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	h.advance(h.step())
	h.mu.Lock()
	h.cfg.Name = "MK4"
	h.mu.Unlock()
	h.advance(h.step())
	if got := h.snap().Printer.URL; got != mk4.srv.URL {
		t.Fatalf("after renaming to MK4 polling %s", got)
	}
}

func TestSwitchingPrinterStartsAFreshBaseline(t *testing.T) {
	a := newStubPrinter(t)
	b := newStubPrinter(t)
	a.set(func(s *stubPrinter) {
		s.status = statusJSON("PRINTING", &jobFields{id: 7, progress: 50, printing: num(600)})
		s.job = jobJSON(7, "PRINTING", "part_a.gcode")
	})
	b.set(func(s *stubPrinter) { s.status = statusJSON("FINISHED", nil) })
	h := newHarness(t, manualConfig(a.srv.URL), testPassword, nil)
	h.advance(h.step())
	h.mu.Lock()
	h.cfg = manualConfig(b.srv.URL)
	h.mu.Unlock()
	h.advance(h.step())
	snap := h.snap()
	if len(snap.Events) != 0 || snap.JobID == 7 || snap.Name == "part_a" || !snap.StartedAt.IsZero() {
		t.Fatalf("printer B inherited printer A's job: %+v", snap)
	}
}

func TestReenablingReplaysNoStaleEdge(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	s.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 1, progress: 50}) })
	h.advance(h.step())
	h.mu.Lock()
	h.cfg.Enabled = false
	h.mu.Unlock()
	h.advance(h.step())
	s.set(func(s *stubPrinter) { s.status = statusJSON("FINISHED", nil) })
	h.advance(3 * time.Hour)
	h.mu.Lock()
	h.cfg.Enabled = true
	h.mu.Unlock()
	h.advance(h.step())
	if ev := eventKinds(h.snap()); len(ev) != 0 {
		t.Fatalf("re-enabling fired %v for a print that ended while disabled", ev)
	}
}

func TestThumbnailRetriesCountPerJob(t *testing.T) {
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) { s.thumbCode = http.StatusInternalServerError })
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	job := func(id int) {
		s.set(func(s *stubPrinter) {
			s.status = statusJSON("PRINTING", &jobFields{id: id, progress: 5})
			s.job = strings.Replace(jobJSON(id, "PRINTING", "a.bgcode"), "BRACKE~1.BGC\",\"display", fmt.Sprintf("J%d.BGC\",\"display", id), 1)
			s.job = strings.Replace(s.job, "/thumb/l/usb/BRACKE~1.BGC", fmt.Sprintf("/thumb/l/usb/J%d.BGC", id), 1)
		})
	}
	job(1)
	h.advance(h.step())
	h.advance(h.step())
	job(2)
	for range 4 {
		h.advance(h.step())
	}
	if got := s.hit("/thumb/l/usb/J2.BGC"); got != thumbTries {
		t.Fatalf("job 2 thumbnail tried %d times, want %d", got, thumbTries)
	}
}

func TestThumbnailFetchedOncePerJob(t *testing.T) {
	s := newStubPrinter(t)
	png := []byte("\x89PNG\r\n\x1a\nthumb")
	s.set(func(s *stubPrinter) {
		s.thumb = png
		s.status = statusJSON("PRINTING", &jobFields{id: 3, progress: 5})
		s.job = jobJSON(3, "PRINTING", "a.bgcode")
	})
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	for i := range 6 {
		s.set(func(s *stubPrinter) {
			s.status = statusJSON([]string{"PRINTING", "PAUSED"}[i%2], &jobFields{id: 3, progress: 5})
		})
		h.advance(h.step())
	}
	if got := s.hit("/thumb/l/usb/BRACKE~1.BGC"); got != 1 {
		t.Fatalf("thumbnail fetched %d times", got)
	}
	b, ver := h.p.State.Thumbnail()
	if string(b) != string(png) || ver == "" || h.snap().ThumbVersion != ver {
		t.Fatalf("thumb = %q ver %q", b, ver)
	}
	if got := s.hit("/api/v1/job"); got != 6 {
		t.Fatalf("job fetched %d times, want once per state change (6)", got)
	}
	s.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 3, progress: 6}) })
	h.advance(h.step())
	s.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 3, progress: 7}) })
	h.advance(h.step())
	h.advance(h.step())
	if got := s.hit("/api/v1/job"); got != 7 {
		t.Fatalf("job fetched %d times without a state or job change", got)
	}
	if got := s.hit("/api/version"); got != 1 {
		t.Fatalf("version fetched %d times", got)
	}
}

func TestMissingThumbnailIsNoPicture(t *testing.T) {
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) {
		s.status = statusJSON("PRINTING", &jobFields{id: 3, progress: 5})
		s.job = jobJSON(3, "PRINTING", "a.bgcode")
	})
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	for i := range 4 {
		s.set(func(s *stubPrinter) {
			s.status = statusJSON([]string{"PRINTING", "PAUSED"}[i%2], &jobFields{id: 3, progress: 5})
		})
		h.advance(h.step())
	}
	if v := h.snap().ThumbVersion; v != "" {
		t.Fatalf("thumb_version = %q", v)
	}
	if got := s.hit("/thumb/l/usb/BRACKE~1.BGC"); got != 1 {
		t.Fatalf("missing thumbnail asked %d times, want once per job", got)
	}
}

func TestDoneHoldOutlastsTheEventWindow(t *testing.T) {
	s := newStubPrinter(t)
	cfg := manualConfig(s.srv.URL)
	cfg.DoneHoldMin = 60
	h := newHarness(t, cfg, testPassword, nil)
	s.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 1, progress: 50}) })
	h.step()
	s.set(func(s *stubPrinter) { s.status = statusJSON("FINISHED", nil) })
	h.advance(PollActive)
	h.step()
	doneAt := h.now
	h.advance(45 * time.Minute)
	if got := h.snap().HoldUntil; !got.Equal(doneAt.Add(time.Hour)) {
		t.Fatalf("60-minute hold at +45 min = %v, want %v", got, doneAt.Add(time.Hour))
	}
	if len(h.snap().Events) != 0 {
		t.Fatal("event kept past 30 min")
	}
}

func TestDoneAndStoppedHolds(t *testing.T) {
	s := newStubPrinter(t)
	cfg := manualConfig(s.srv.URL)
	cfg.DoneHoldMin = 20
	h := newHarness(t, cfg, testPassword, nil)
	s.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 1, progress: 50}) })
	h.step()
	s.set(func(s *stubPrinter) { s.status = statusJSON("FINISHED", nil) })
	h.advance(PollActive)
	h.step()
	doneAt := h.now
	if got := h.snap().HoldUntil; !got.Equal(doneAt.Add(20 * time.Minute)) {
		t.Fatalf("done hold until %v", got)
	}
	h.advance(20*time.Minute + time.Second)
	if got := h.snap().HoldUntil; !got.IsZero() {
		t.Fatalf("hold still on after done_hold_min: %v", got)
	}
	if h.snap().Phase != PhaseFinished {
		t.Fatal("phase left finished")
	}

	s.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 2, progress: 50}) })
	h.step()
	s.set(func(s *stubPrinter) { s.status = statusJSON("STOPPED", nil) })
	h.advance(PollActive)
	h.step()
	if got := h.snap().HoldUntil; !got.Equal(h.now.Add(StoppedHold)) {
		t.Fatalf("stopped hold until %v", got)
	}
}

func TestEventsExpireAfterThirtyMinutes(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	st.observe(now, StatusDoc{Printer: printerFields("PRINTING"), Job: &StatusJob{ID: 1, Progress: 50}})
	st.observe(now.Add(time.Minute), StatusDoc{Printer: printerFields("FINISHED")})
	if s := st.At(now.Add(30 * time.Minute)); s.Event == nil || s.Event.Kind != EventFinished {
		t.Fatalf("event = %+v", s.Event)
	}
	if s := st.At(now.Add(32 * time.Minute)); s.Event != nil || len(s.Events) != 0 {
		t.Fatalf("event kept past 30 min: %+v", s.Events)
	}
}

func printerFields(state string) StatusPrinter { return StatusPrinter{State: state} }

func TestOnChangeFiresOnPhaseNotOnSteadyPolls(t *testing.T) {
	s := newStubPrinter(t)
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	changes := 0
	h.p.OnChange = func() { changes++ }
	s.set(func(s *stubPrinter) {
		s.status = statusJSON("PRINTING", &jobFields{id: 1, progress: 40.2, remaining: num(600), nozzle: 240, nozzleT: 240})
	})
	h.advance(h.step())
	first := changes
	for i := range 4 {
		s.set(func(s *stubPrinter) {
			s.status = statusJSON("PRINTING", &jobFields{id: 1, progress: 40.3 + float64(i)/10, remaining: num(600 - float64(5*(i+1))), nozzle: 240 + float64(i), nozzleT: 240})
		})
		h.advance(h.step())
	}
	if changes != first {
		t.Fatalf("steady polls fired %d changes", changes-first)
	}
	s.set(func(s *stubPrinter) {
		s.status = statusJSON("PRINTING", &jobFields{id: 1, progress: 41.0, remaining: num(570)})
	})
	h.step()
	if changes != first+1 {
		t.Fatalf("a whole-percent step fired %d changes", changes-first)
	}
}

func TestPasswordNeverLeaks(t *testing.T) {
	s := newStubPrinter(t)
	png := []byte("\x89PNG\r\n\x1a\nthumb")
	s.set(func(s *stubPrinter) {
		s.thumb = png
		s.status = statusJSON("PRINTING", &jobFields{id: 3, progress: 5})
		s.job = jobJSON(3, "PRINTING", "a.bgcode")
	})
	var seen []string
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	for range 3 {
		h.advance(h.step())
	}
	wrongSrv := newStubPrinter(t)
	wrongSrv.set(func(s *stubPrinter) { s.password = "different" })
	hw := newHarness(t, manualConfig(wrongSrv.srv.URL), testPassword, nil)
	hw.step()
	hd := newHarness(t, manualConfig("http://127.0.0.1:1"), testPassword, nil)
	for range 3 {
		hd.advance(hd.step())
	}
	for _, x := range []*harness{h, hw, hd} {
		seen = append(seen, x.logs.String())
		b, err := json.Marshal(x.snap())
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, string(b), fmt.Sprintf("%v %+v %#v", x.snap(), x.snap(), x.snap()))
	}
	creds := Credentials{User: DefaultUser, Password: testPassword}
	seen = append(seen, fmt.Sprintf("%v %+v %#v %s", creds, creds, creds, creds))
	c := newTestClient(t, wrongSrv.srv.URL, testPassword)
	_, err := c.Status(context.Background())
	seen = append(seen, err.Error())
	c = newTestClient(t, "http://127.0.0.1:1", testPassword)
	_, err = c.Status(context.Background())
	seen = append(seen, err.Error())
	for i, text := range seen {
		if strings.Contains(text, testPassword) {
			t.Errorf("output %d contains the password: %s", i, text)
		}
	}

	if real := os.Getenv(PasswordEnv); real != "" {
		files, _ := filepath.Glob("*")
		testdata, _ := filepath.Glob("testdata/*")
		for _, f := range append(files, testdata...) {
			b, err := os.ReadFile(f)
			if err == nil && strings.Contains(string(b), real) {
				t.Errorf("%s contains the real %s", f, PasswordEnv)
			}
		}
	}
}

func TestShortName(t *testing.T) {
	cases := []struct{ in, name, material string }{
		{"bracket_v3_0.4n_0.2mm_PETG_COREONE_2h14m.bgcode", "bracket_v3", "PETG"},
		{"Benchy_0.6n_0.25mm_PLA_COREONE_45m.gcode", "Benchy", "PLA"},
		{"multi_0.4n_0.2mm_PLA;PETG_COREONEL_1d2h3m.bgcode", "multi", "PLA/PETG"},
		{"my_part.bgcode", "my_part", ""},
		{"BRACKE~1.BGC", "BRACKE~1", ""},
		{"a_very_long_file_name_that_goes_past_thirty_two.gcode", "a_very_long_file_name_that_goes_", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		name, material := ShortName(c.in)
		if name != c.name || material != c.material {
			t.Errorf("ShortName(%q) = %q, %q; want %q, %q", c.in, name, material, c.name, c.material)
		}
	}
}

func TestDerivePhaseTable(t *testing.T) {
	doc := func(state string, job *StatusJob, nozzle, nozzleT, bed, bedT float64) StatusDoc {
		d := StatusDoc{Job: job}
		d.Printer.State, d.Printer.TempNozzle, d.Printer.TargetNozzle, d.Printer.TempBed, d.Printer.TargetBed = state, nozzle, nozzleT, bed, bedT
		return d
	}
	j := func(p float64) *StatusJob { return &StatusJob{ID: 1, Progress: p} }
	cases := []struct {
		d    StatusDoc
		want Phase
	}{
		{doc("IDLE", nil, 0, 0, 0, 0), PhaseIdle},
		{doc("READY", nil, 0, 0, 0, 0), PhaseIdle},
		{doc("BUSY", nil, 0, 0, 0, 0), PhaseIdle},
		{doc("PRINTING", j(0), 200, 240, 85, 85), PhaseHeating},
		{doc("PRINTING", j(0), 240, 240, 70, 85), PhaseHeating},
		{doc("PRINTING", j(0.5), 238, 240, 83, 85), PhasePreparing},
		{doc("PRINTING", j(0), 25, 0, 25, 0), PhasePreparing},
		{doc("PRINTING", j(1), 240, 240, 85, 85), PhasePrinting},
		{doc("PRINTING", j(99.9), 240, 240, 85, 85), PhasePrinting},
		{doc("PRINTING", j(100), 200, 0, 60, 0), PhaseFinishing},
		{doc("PAUSED", j(40), 0, 0, 0, 0), PhasePaused},
		{doc("ATTENTION", j(40), 0, 0, 0, 0), PhaseAttention},
		{doc("FINISHED", nil, 0, 0, 0, 0), PhaseFinished},
		{doc("STOPPED", nil, 0, 0, 0, 0), PhaseStopped},
		{doc("ERROR", nil, 0, 0, 0, 0), PhaseError},
	}
	for _, c := range cases {
		if got := DerivePhase(c.d); got != c.want {
			t.Errorf("DerivePhase(%s %+v) = %s, want %s", c.d.Printer.State, c.d.Job, got, c.want)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		url  string
		hold int
		ok   bool
	}{
		{"auto", 30, true},
		{"http://192.168.0.50", 30, true},
		{"http://prusa-core-one.local:80/", 30, true},
		{"https://printer", 1, true},
		{"ftp://printer", 30, false},
		{"http://maker:pw@printer", 30, false},
		{"http://printer/api", 30, false},
		{"printer", 30, false},
		{"auto", 0, true},
		{"auto", -1, false},
		{"auto", 24*60 + 1, false},
	}
	for _, c := range cases {
		cfg := Config{URL: c.url}
		cfg.FillAbsent()
		cfg.URL = strings.TrimRight(cfg.URL, "/")
		cfg.DoneHoldMin = c.hold
		if err := cfg.Validate(); (err == nil) != c.ok {
			t.Errorf("Validate(url=%q hold=%d) = %v, want ok=%v", c.url, c.hold, err, c.ok)
		}
	}
}

func TestAutoModeNeverSwitchesToAPartialNameNeighbour(t *testing.T) {
	core := newStubPrinter(t)
	coreL := newStubPrinter(t)
	coreL.set(func(s *stubPrinter) { s.status = statusJSON("FINISHED", nil) })
	net := &lan{}
	net.set(Found{Host: "prusa-core-one.local.", URL: core.srv.URL}, Found{Host: "prusa-core-one-l.local.", URL: coreL.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	core.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 7, progress: 50}) })
	h.advance(h.step())
	core.srv.Close()
	net.set(Found{Host: "prusa-core-one-l.local.", URL: coreL.srv.URL})
	for range 5 {
		h.advance(h.step())
	}
	if snap := h.snap(); snap.Printer.URL != core.srv.URL || snap.Phase != PhaseOffline || coreL.total() != 0 {
		t.Fatalf("moved to %s (%s), CORE One L requests %d", snap.Printer.URL, snap.Phase, coreL.total())
	}
}

func TestFallbackPrinterKeepsItsNonceAndBrowsesSparingly(t *testing.T) {
	s := newStubPrinter(t)
	net := &lan{}
	net.set(Found{Name: "Workshop", Host: "workshop.local.", URL: s.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	for range 10 {
		h.advance(h.step())
	}
	if net.n > 2 {
		t.Fatalf("%d browses over 10 idle polls (5 min)", net.n)
	}
	if s.unauth != 1 {
		t.Fatalf("%d challenges over 10 polls, want the nonce kept (1)", s.unauth)
	}
}

func TestWrongPasswordRetriesAfterANameChange(t *testing.T) {
	core := newStubPrinter(t)
	core.set(func(s *stubPrinter) { s.password = "core-password" })
	mk4 := newStubPrinter(t)
	net := &lan{}
	net.set(Found{Host: "prusa-core-one.local.", URL: core.srv.URL}, Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	h.advance(h.step())
	if h.snap().LastError != ErrCodeWrongPassword {
		t.Fatal("want wrong_password on the CORE One")
	}
	h.mu.Lock()
	h.cfg.Name = "MK4"
	h.mu.Unlock()
	h.advance(h.step())
	if snap := h.snap(); snap.Printer.URL != mk4.srv.URL || snap.LastError != ErrCodeNone || snap.Phase != PhaseIdle {
		t.Fatalf("after renaming: %s %s %q", snap.Printer.URL, snap.Phase, snap.LastError)
	}
}

func TestWrongPasswordOnAFallbackStillFindsTheNamedPrinter(t *testing.T) {
	mk4 := newStubPrinter(t)
	mk4.set(func(s *stubPrinter) { s.password = "mk4-password" })
	core := newStubPrinter(t)
	net := &lan{}
	net.set(Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	h.advance(h.step())
	if h.snap().LastError != ErrCodeWrongPassword {
		t.Fatal("want wrong_password on the fallback")
	}
	net.set(Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL}, Found{Host: "prusa-core-one.local.", URL: core.srv.URL})
	h.advance(FallbackBrowse)
	h.advance(h.step())
	if snap := h.snap(); snap.Printer.URL != core.srv.URL || snap.LastError != ErrCodeNone {
		t.Fatalf("named printer not found after a fallback's wrong password: %s %q", snap.Printer.URL, snap.LastError)
	}
}

func TestVersionRetriedAfterAFailure(t *testing.T) {
	s := newStubPrinter(t)
	version := ""
	s.set(func(s *stubPrinter) { version, s.version = s.version, "" })
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	h.advance(h.step())
	s.set(func(s *stubPrinter) { s.version = version })
	h.advance(h.step())
	if got := h.snap().Printer.Firmware; got != "8.0.0+12345" {
		t.Fatalf("firmware = %q after one failed /api/version", got)
	}
}

func TestThumbnailRetriesResetForAReprintOfTheSameFile(t *testing.T) {
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) { s.thumbCode = http.StatusInternalServerError })
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	job := func(id int) {
		s.set(func(s *stubPrinter) {
			s.status = statusJSON("PRINTING", &jobFields{id: id, progress: 5})
			s.job = jobJSON(id, "PRINTING", "a.bgcode")
		})
	}
	job(1)
	h.advance(h.step())
	h.advance(h.step())
	before := s.hit("/thumb/l/usb/BRACKE~1.BGC")
	job(2)
	for range 4 {
		h.advance(h.step())
	}
	if got := s.hit("/thumb/l/usb/BRACKE~1.BGC") - before; got != thumbTries {
		t.Fatalf("reprint tried the thumbnail %d times, want %d", got, thumbTries)
	}
}

func TestRestartDuringPrintPreviewStillEmitsStarted(t *testing.T) {
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) { s.status = statusJSON("ATTENTION", &jobFields{id: 5, progress: 0}) })
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	h.advance(h.step())
	s.set(func(s *stubPrinter) {
		s.status = statusJSON("PRINTING", &jobFields{id: 5, progress: 0, nozzle: 100, nozzleT: 240})
	})
	h.advance(h.step())
	if ev := eventKinds(h.snap()); !slices.Equal(ev, []EventKind{EventStarted}) {
		t.Fatalf("events = %v", ev)
	}
}

func TestAutoModePrinterOffGoesOfflineAndBacksOff(t *testing.T) {
	s := newStubPrinter(t)
	net := &lan{}
	net.set(Found{Name: "Prusa CORE One", Host: "prusa-core-one.local.", URL: s.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	h.advance(h.step())
	s.srv.Close()
	var delays []time.Duration
	for range 5 {
		d := h.step()
		delays = append(delays, d)
		h.advance(d)
	}
	want := []time.Duration{PollIdle, 30 * time.Second, 60 * time.Second, 120 * time.Second, 300 * time.Second}
	if !slices.Equal(delays, want) || h.snap().Phase != PhaseOffline || h.snap().LastError != ErrCodeUnreachable {
		t.Fatalf("delays %v phase %s error %q", delays, h.snap().Phase, h.snap().LastError)
	}
	warns := 0
	for _, l := range h.logs.lines() {
		if strings.Contains(l, "level=WARN") {
			warns++
		}
	}
	if warns != 1 {
		t.Fatalf("warn lines = %d", warns)
	}
}

func TestUnusableThumbnailIsNotRetried(t *testing.T) {
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) {
		s.thumb, s.thumbType = []byte("\x89PNG\r\n\x1a\nx"), "image/jpeg"
		s.status = statusJSON("PRINTING", &jobFields{id: 3, progress: 5})
		s.job = jobJSON(3, "PRINTING", "a.bgcode")
	})
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	for range 4 {
		h.advance(h.step())
	}
	if got := s.hit("/thumb/l/usb/BRACKE~1.BGC"); got != 1 {
		t.Fatalf("unusable thumbnail fetched %d times", got)
	}
}

func TestFallbackThrottleHoldsWhenMDNSGoesQuiet(t *testing.T) {
	s := newStubPrinter(t)
	net := &lan{}
	net.set(Found{Host: "workshop.local.", URL: s.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	h.advance(h.step())
	net.set()
	for range 20 {
		h.advance(h.step())
	}
	if net.n > 3 {
		t.Fatalf("%d browses in 10 min with mDNS quiet", net.n)
	}
}

func TestPrintingFallbackStillYieldsToTheNamedPrinter(t *testing.T) {
	mk4 := newStubPrinter(t)
	core := newStubPrinter(t)
	mk4.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 1, progress: 30}) })
	core.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 2, progress: 10}) })
	net := &lan{}
	net.set(Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	h.advance(h.step())
	net.set(Found{Host: "prusa-mk4.local.", URL: mk4.srv.URL}, Found{Host: "prusa-core-one.local.", URL: core.srv.URL})
	for range 61 {
		h.advance(h.step())
	}
	if got := h.snap().Printer.URL; got != core.srv.URL {
		t.Fatalf("after 5 min of active polls still on %s", got)
	}
}

func TestNotFoundAndDisableClearTheOldAddress(t *testing.T) {
	s := newStubPrinter(t)
	net := &lan{}
	net.set(Found{Host: "prusa-core-one.local.", URL: s.srv.URL})
	h := newHarness(t, Config{Enabled: true, URL: URLAuto, Name: "CORE One"}, testPassword, net.browse)
	h.advance(h.step())
	net.set()
	h.mu.Lock()
	h.cfg.Name = "MK4"
	h.mu.Unlock()
	h.advance(h.step())
	if snap := h.snap(); snap.LastError != ErrCodeNotFound || snap.Printer.URL != "" || snap.Printer.Host != "" {
		t.Fatalf("not_found keeps %+v", snap.Printer)
	}
	h2 := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	h2.advance(h2.step())
	h2.mu.Lock()
	h2.cfg.Enabled = false
	h2.mu.Unlock()
	h2.advance(h2.step())
	if got := h2.snap().Printer; got.URL != "" || got.Host != "" || got.Firmware != "" {
		t.Fatalf("disabled keeps %+v", got)
	}
}

func TestNewManualURLStartsWithAFreshFailureCount(t *testing.T) {
	dead := newStubPrinter(t)
	h := newHarness(t, manualConfig(dead.srv.URL), testPassword, nil)
	h.advance(h.step())
	dead.srv.Close()
	for range 4 {
		h.advance(h.step())
	}
	other := newStubPrinter(t)
	h.mu.Lock()
	h.cfg = manualConfig(other.srv.URL)
	h.mu.Unlock()
	other.srv.Close()
	first := h.step()
	h.advance(first)
	second := h.step()
	if first != PollIdle || second != Backoff[0] {
		t.Fatalf("new URL: polls after %v then %v, want %v then %v", first, second, PollIdle, Backoff[0])
	}
}

func TestBrowseErrorIsLogged(t *testing.T) {
	h := newHarness(t, Config{Enabled: true, URL: URLAuto}, testPassword, func(context.Context) ([]Found, error) {
		return nil, fmt.Errorf("listen udp 224.0.0.251:5353: permission denied")
	})
	h.step()
	if !strings.Contains(h.logs.String(), "permission denied") {
		t.Fatalf("browse error not logged:\n%s", h.logs.String())
	}
}
