package sessions

import (
	"slices"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// fakeClock is a hand-moved clock for the registry.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// harness is a registry on a fake clock with a fixed policy, recording reaps.
type harness struct {
	*Registry
	clk    *fakeClock
	policy Policy
	reaped []Reaped
}

func newHarness() *harness {
	h := &harness{
		clk:    &fakeClock{t: time.Unix(1_000_000, 0)},
		policy: Policy{StaleAfter: 25 * time.Second, DoneTTL: 30 * time.Second},
	}
	h.Registry = New(h.clk.Now, func() Policy { return h.policy }, func(r Reaped) { h.reaped = append(h.reaped, r) })
	return h
}

func sess(source, state string) render.Session {
	return render.Session{Source: source, Tool: "claude", Session: "s", State: state}
}

func keys(v View) []string {
	out := make([]string, len(v.Sessions))
	for i, s := range v.Sessions {
		out[i] = s.Source
	}
	return out
}

func TestStalenessPerState(t *testing.T) {
	cases := []struct {
		state string
		age   time.Duration
		live  bool
	}{
		{"running", 25 * time.Second, true},
		{"running", 26 * time.Second, false},
		{"waiting", 25 * time.Second, true},
		{"waiting", 26 * time.Second, false},
		{"idle", 26 * time.Second, false},
		{"done", 28 * time.Second, true},
		{"done", 30 * time.Second, true},
		{"done", 31 * time.Second, false},
		{"error", 28 * time.Second, true},
		{"error", 31 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.state+"/"+tc.age.String(), func(t *testing.T) {
			h := newHarness()
			h.Upsert(sess("a", tc.state))
			h.clk.Advance(tc.age)

			v := h.View()
			if got := len(v.Sessions) == 1; got != tc.live {
				t.Fatalf("live = %v, want %v", got, tc.live)
			}
			if tc.live {
				if len(h.reaped) != 0 {
					t.Fatalf("reaped %v, want none", h.reaped)
				}
				return
			}
			if len(h.reaped) != 1 || h.reaped[0].Session.Source != "a" || h.reaped[0].Age != tc.age {
				t.Fatalf("reaped = %+v, want one reap of a at age %v", h.reaped, tc.age)
			}
		})
	}
}

// Reaping belongs to the registry, not to any render: every access reaps.
func TestEveryAccessReaps(t *testing.T) {
	ops := map[string]func(h *harness){
		"View":   func(h *harness) { h.View() },
		"Upsert": func(h *harness) { h.Upsert(sess("fresh", "running")) },
		"Delete": func(h *harness) { h.Delete("missing/claude/s") },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			h.Upsert(sess("old", "running"))
			h.clk.Advance(26 * time.Second)
			op(h)
			if len(h.reaped) != 1 {
				t.Fatalf("%s reaped %d sessions, want 1", name, len(h.reaped))
			}
			for _, s := range h.View().Sessions {
				if s.Source == "old" {
					t.Fatalf("stale session still visible after %s", name)
				}
			}
		})
	}
}

func TestUpsertStampsClockAndReportsPrior(t *testing.T) {
	h := newHarness()
	v, prior := h.Upsert(sess("a", "running"))
	if prior != "" {
		t.Fatalf("prior = %q, want \"\" for a new session", prior)
	}
	if got := v.Sessions[0].UpdatedAt; !got.Equal(h.clk.Now()) {
		t.Fatalf("UpdatedAt = %v, want the registry clock %v", got, h.clk.Now())
	}
	if !v.Now.Equal(h.clk.Now()) {
		t.Fatalf("View.Now = %v, want %v", v.Now, h.clk.Now())
	}

	h.clk.Advance(10 * time.Second)
	v, prior = h.Upsert(sess("a", "waiting"))
	if prior != "running" {
		t.Fatalf("prior = %q, want running", prior)
	}
	if len(v.Sessions) != 1 || v.Sessions[0].State != "waiting" {
		t.Fatalf("sessions = %+v, want one waiting session", v.Sessions)
	}

	// A session that went stale is gone before the next upsert: it comes back new.
	h.clk.Advance(26 * time.Second)
	if _, prior = h.Upsert(sess("a", "running")); prior != "" {
		t.Fatalf("prior after going stale = %q, want \"\"", prior)
	}
}

func TestViewIsNewestFirst(t *testing.T) {
	h := newHarness()
	for _, src := range []string{"a", "b", "c"} {
		h.Upsert(sess(src, "running"))
		h.clk.Advance(time.Second)
	}
	h.Upsert(sess("a", "running")) // refresh a
	if got, want := keys(h.View()), []string{"a", "c", "b"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestDeleteAndClear(t *testing.T) {
	h := newHarness()
	h.Upsert(sess("a", "running"))
	h.Upsert(sess("b", "waiting"))

	v := h.Delete(sess("a", "").Key())
	if got := keys(v); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("after Delete = %v, want [b]", got)
	}
	h.Delete("never/was/here") // idempotent

	if v := h.Clear(); len(v.Sessions) != 0 {
		t.Fatalf("after Clear = %v, want none", keys(v))
	}
	if len(h.reaped) != 0 {
		t.Fatalf("Delete/Clear counted as reaps: %+v", h.reaped)
	}
}

// The policy is read per access, so a config hot-reload applies immediately.
func TestPolicyIsReadPerAccess(t *testing.T) {
	h := newHarness()
	h.Upsert(sess("a", "running"))
	h.clk.Advance(10 * time.Second)
	if len(h.View().Sessions) != 1 {
		t.Fatal("session should be live at 10s under stale_seconds=25")
	}
	h.policy.StaleAfter = 5 * time.Second
	if len(h.View().Sessions) != 0 {
		t.Fatal("session should be reaped at 10s once stale_seconds drops to 5")
	}
}

func TestViewWinnerAndCount(t *testing.T) {
	h := newHarness()
	if w := h.View().Winner(); w != nil {
		t.Fatalf("empty registry winner = %+v, want nil", w)
	}
	h.Upsert(sess("idle", "idle"))
	if w := h.View().Winner(); w != nil {
		t.Fatalf("idle-only winner = %+v, want nil", w)
	}
	h.Upsert(sess("r1", "running"))
	h.clk.Advance(time.Second)
	h.Upsert(sess("r2", "running"))
	h.Upsert(sess("d", "done"))
	v := h.View()
	if w := v.Winner(); w == nil || w.Source != "r2" {
		t.Fatalf("winner = %+v, want the newest running session r2", w)
	}
	h.Upsert(sess("w", "waiting"))
	v = h.View()
	if w := v.Winner(); w == nil || w.Source != "w" {
		t.Fatalf("winner = %+v, want waiting session w", w)
	}
	for state, want := range map[string]int{"running": 2, "waiting": 1, "done": 1, "idle": 1, "error": 0} {
		if got := v.Count(state); got != want {
			t.Errorf("Count(%s) = %d, want %d", state, got, want)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := New(time.Now, func() Policy { return Policy{StaleAfter: time.Minute, DoneTTL: time.Minute} }, nil)
	done := make(chan struct{})
	for i := range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := range 100 {
				s := sess(string(rune('a'+i)), "running")
				r.Upsert(s)
				r.View()
				if j%10 == 0 {
					r.Delete(s.Key())
				}
			}
		}()
	}
	for range 8 {
		<-done
	}
}

// A nil onReap is allowed.
func TestNilOnReap(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	r := New(clk.Now, func() Policy { return Policy{StaleAfter: time.Second, DoneTTL: time.Second} }, nil)
	r.Upsert(sess("a", "running"))
	clk.Advance(2 * time.Second)
	if len(r.View().Sessions) != 0 {
		t.Fatal("want the session reaped")
	}
}
