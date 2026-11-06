package printer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

type traceRow struct {
	T          float64         `json:"t"`
	StatusCode int             `json:"status_code"`
	Status     json.RawMessage `json:"status"`
	JobCode    int             `json:"job_code"`
	Job        json.RawMessage `json:"job"`
}

func loadTrace(t *testing.T, name string) []traceRow {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []traceRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r traceRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != 200 || (r.JobCode != 200 && r.JobCode != 204) {
			t.Fatalf("trace row at %v: status %d job %d", r.T, r.StatusCode, r.JobCode)
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func (s *stubPrinter) play(r traceRow) {
	s.set(func(s *stubPrinter) {
		s.status, s.job = string(r.Status), ""
		if r.JobCode == 200 {
			s.job = string(r.Job)
		}
	})
}

func (h *harness) at(sec float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = time.Unix(0, int64(sec*float64(time.Second))).UTC()
}

func replay(t *testing.T, s *stubPrinter, h *harness, rows []traceRow) []Snapshot {
	t.Helper()
	out := make([]Snapshot, 0, len(rows))
	for _, r := range rows {
		s.play(r)
		h.at(r.T)
		h.step()
		out = append(out, h.snap())
	}
	return out
}

const (
	sampleDisplayName = "sample-part_0.2mm_PLA_COREONE_6m.bgcode"
	remoteTrace       = "core-one-remote-print.jsonl"
	screenTrace       = "core-one-screen-print-stopped.jsonl"
)

func coarsePhases(snaps []Snapshot) []Phase {
	var out []Phase
	for _, sn := range snaps {
		p := sn.Phase
		if p == PhaseHeating {
			p = PhasePreparing
		}
		if len(out) == 0 || out[len(out)-1] != p {
			out = append(out, p)
		}
	}
	return out
}

type edge struct {
	Kind EventKind
	Job  int
	Row  int
}

func edgesOf(snaps []Snapshot) []edge {
	var out []edge
	var last uint64
	for i, sn := range snaps {
		for _, e := range sn.Events {
			if e.ID > last {
				out = append(out, edge{e.Kind, e.JobID, i})
				last = e.ID
			}
		}
	}
	return out
}

func playTrace(t *testing.T, name string) (*stubPrinter, *harness, []traceRow, []Snapshot) {
	t.Helper()
	rows := loadTrace(t, name)
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) { s.thumb = testPNG(t, 1) })
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	return s, h, rows, replay(t, s, h, rows)
}

func firstRunning(snaps []Snapshot) int {
	for i, sn := range snaps {
		if isRunning(sn.Phase) {
			return i
		}
	}
	return -1
}

func startedAtOf(t *testing.T, row traceRow) time.Time {
	t.Helper()
	var d struct {
		Job struct {
			TimePrinting float64 `json:"time_printing"`
		} `json:"job"`
	}
	if err := json.Unmarshal(row.Status, &d); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(0, int64(row.T*float64(time.Second))).UTC()
	return at.Add(-time.Duration(d.Job.TimePrinting) * time.Second).Truncate(time.Second)
}

func rowOf(snaps []Snapshot, phase Phase) int {
	for i, sn := range snaps {
		if sn.Phase == phase {
			return i
		}
	}
	return -1
}

func TestReplayRemotePrintYieldsPhasesAndOneEventPerEdge(t *testing.T) {
	_, h, rows, snaps := playTrace(t, remoteTrace)
	if rowOf(snaps, PhaseHeating) < 0 {
		t.Fatal("the preheat never read heating")
	}
	want := []Phase{PhaseIdle, PhasePreparing, PhasePrinting, PhasePaused, PhasePrinting, PhaseFinishing, PhaseFinished, PhaseIdle}
	if got := coarsePhases(snaps); !slices.Equal(got, want) {
		t.Fatalf("phases = %v\nwant     %v", got, want)
	}
	edges := edgesOf(snaps)
	if len(edges) != 2 || edges[0].Kind != EventStarted || edges[0].Job != 694 || edges[1].Kind != EventFinished || edges[1].Job != 694 {
		t.Fatalf("events = %+v, want started and finished of job 694", edges)
	}
	first := firstRunning(snaps)
	if first != edges[0].Row {
		t.Fatalf("started at row %d, the print began at row %d", edges[0].Row, first)
	}
	last := h.snap()
	for i := 1; i < len(last.Events); i++ {
		if last.Events[i].ID <= last.Events[i-1].ID {
			t.Fatalf("event ids not increasing: %+v", last.Events)
		}
	}
	done := snaps[edges[1].Row]
	if name, _ := ShortName(sampleDisplayName); done.Phase != PhaseFinished || done.Name != name || done.JobID != 694 || done.Progress != 100 || done.ThumbVersion == "" {
		t.Fatalf("finished snapshot = %+v", done)
	}
	if want := done.Events[1].At.Add(30 * time.Minute); !done.HoldUntil.Equal(want) {
		t.Fatalf("hold until %v, want %v", done.HoldUntil, want)
	}
	for _, sn := range snaps[edges[1].Row:] {
		if sn.Phase == PhaseFinished && (sn.Name != done.Name || sn.JobID != 694 || sn.ThumbVersion != done.ThumbVersion || !sn.HoldUntil.Equal(done.HoldUntil)) {
			t.Fatalf("the hold lost the job: %+v", sn)
		}
	}
	idle := snaps[len(snaps)-1]
	if idle.Phase != PhaseIdle || idle.JobID != 0 || idle.Name != "" || idle.ThumbVersion != "" || !idle.HoldUntil.IsZero() || idle.StartedAt != (time.Time{}) {
		t.Fatalf("idle after the finish screen = %+v", idle)
	}
	if p := rowOf(snaps, PhasePaused); snaps[p].Event.Kind != EventStarted {
		t.Fatalf("the pause fired %+v", snaps[p].Event)
	}
	if got, want := snaps[first].StartedAt, startedAtOf(t, rows[first]); !got.Equal(want) {
		t.Fatalf("a remote start's started_at = %v, want %v", got, want)
	}
}

func TestReplayScreenStartedPrintStopsWithOneEventPerEdge(t *testing.T) {
	_, _, rows, snaps := playTrace(t, screenTrace)
	want := []Phase{PhaseIdle, PhaseAttention, PhasePreparing, PhasePrinting, PhaseStopped, PhaseIdle}
	if got := coarsePhases(snaps); !slices.Equal(got, want) {
		t.Fatalf("phases = %v\nwant     %v", got, want)
	}
	edges := edgesOf(snaps)
	kinds := make([]EventKind, len(edges))
	for i, e := range edges {
		kinds[i] = e.Kind
		if e.Job != 695 {
			t.Fatalf("event %+v is not job 695", e)
		}
	}
	if !slices.Equal(kinds, []EventKind{EventAttention, EventStarted, EventStopped}) {
		t.Fatalf("events = %v", kinds)
	}
	for i, sn := range snaps {
		if sn.Phase == PhaseAttention && !sn.StartedAt.IsZero() {
			t.Fatalf("row %d: the print preview anchored started_at to the last print's time: %v", i, sn.StartedAt)
		}
	}
	first := firstRunning(snaps)
	if first != edges[1].Row {
		t.Fatalf("started at row %d, the print began at row %d", edges[1].Row, first)
	}
	if want := startedAtOf(t, rows[first]); !snaps[first].StartedAt.Equal(want) {
		t.Fatalf("started_at = %v, want %v", snaps[first].StartedAt, want)
	}
	stopped := snaps[edges[2].Row]
	if name, _ := ShortName(sampleDisplayName); stopped.Phase != PhaseStopped || stopped.Name != name || stopped.JobID != 695 || stopped.ThumbVersion == "" || stopped.Progress < 50 {
		t.Fatalf("stopped snapshot = %+v", stopped)
	}
	if want := stopped.Events[2].At.Add(StoppedHold); !stopped.HoldUntil.Equal(want) {
		t.Fatalf("hold until %v, want %v", stopped.HoldUntil, want)
	}
	idle := snaps[len(snaps)-1]
	if idle.Phase != PhaseIdle || idle.JobID != 0 || idle.Name != "" || !idle.HoldUntil.IsZero() {
		t.Fatalf("idle after the stopped screen = %+v", idle)
	}
}

func TestReplayOnePrintAfterTheOtherFiresEachEdgeOnce(t *testing.T) {
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) { s.thumb = testPNG(t, 1) })
	h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
	var snaps []Snapshot
	for _, name := range []string{remoteTrace, screenTrace} {
		snaps = append(snaps, replay(t, s, h, loadTrace(t, name))...)
	}
	var kinds []EventKind
	for _, e := range edgesOf(snaps) {
		kinds = append(kinds, e.Kind)
	}
	if want := []EventKind{EventStarted, EventFinished, EventAttention, EventStarted, EventStopped}; !slices.Equal(kinds, want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
}

func TestReplayRestartAtEveryRowReplaysNoEndedOrRunningEdge(t *testing.T) {
	for _, name := range []string{remoteTrace, screenTrace} {
		t.Run(name, func(t *testing.T) {
			s, _, rows, full := playTrace(t, name)
			screen := loadTrace(t, screenTrace)
			fullEdges := edgesOf(full)
			for i := range rows {
				h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
				snaps := replay(t, s, h, rows[i:])
				var want, got []edge
				for _, e := range fullEdges {
					if e.Row > i {
						want = append(want, edge{e.Kind, e.Job, 0})
					}
				}
				for _, e := range edgesOf(snaps) {
					got = append(got, edge{e.Kind, e.Job, 0})
				}
				if !slices.Equal(got, want) {
					t.Fatalf("restart at row %d (%s): events %v, want %v", i, full[i].Phase, got, want)
				}
				for k, sn := range snaps {
					if len(edgesOf(snaps[:k+1])) == 0 && !sn.HoldUntil.IsZero() {
						t.Fatalf("restart at row %d: hold at row %d before any edge", i, i+k)
					}
				}
				if first := firstRunning(full); i < first {
					if got, want := snaps[first-i].StartedAt, full[first].StartedAt; !got.Equal(want) {
						t.Fatalf("restart at row %d: started_at %v, want %v", i, got, want)
					}
				}
				for k, sn := range snaps {
					if sn.Phase == PhasePrinting && full[i+k].Phase == PhasePrinting && sn.StartedAt.Sub(full[i+k].StartedAt).Abs() > AnchorTolerance {
						t.Fatalf("restart at row %d: started_at %v at row %d, the full replay has %v", i, sn.StartedAt, i+k, full[i+k].StartedAt)
					}
				}
				if name == remoteTrace {
					var lastID uint64
					if ev := snaps[len(snaps)-1].Events; len(ev) > 0 {
						lastID = ev[len(ev)-1].ID
					}
					var got []edge
					for _, e := range replay(t, s, h, screen)[len(screen)-1].Events {
						if e.ID > lastID {
							got = append(got, edge{e.Kind, e.JobID, 0})
						}
					}
					if want := []edge{{EventAttention, 695, 0}, {EventStarted, 695, 0}, {EventStopped, 695, 0}}; !slices.Equal(got, want) {
						t.Fatalf("restart at row %d: the next print fired %v", i, got)
					}
				}
				if end := snaps[len(snaps)-1]; end.Phase != full[len(full)-1].Phase || end.JobID != full[len(full)-1].JobID {
					t.Fatalf("restart at row %d ends in %s job %d", i, end.Phase, end.JobID)
				}
			}
		})
	}
}

func TestReplayEveryThirdRowYieldsTheSameEvents(t *testing.T) {
	for _, name := range []string{remoteTrace, screenTrace} {
		_, _, _, full := playTrace(t, name)
		var want []EventKind
		for _, e := range edgesOf(full) {
			want = append(want, e.Kind)
		}
		for offset := range 3 {
			t.Run(fmt.Sprintf("%s/%d", name, offset), func(t *testing.T) {
				s := newStubPrinter(t)
				s.set(func(s *stubPrinter) { s.thumb = testPNG(t, 1) })
				h := newHarness(t, manualConfig(s.srv.URL), testPassword, nil)
				var every []traceRow
				for i, r := range loadTrace(t, name) {
					if i%3 == offset {
						every = append(every, r)
					}
				}
				snaps := replay(t, s, h, every)
				var got []EventKind
				for _, e := range edgesOf(snaps) {
					got = append(got, e.Kind)
				}
				if !slices.Equal(got, want) {
					t.Fatalf("offset %d: events %v, want %v", offset, got, want)
				}
			})
		}
	}
}
