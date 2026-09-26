// Package sessions is the server's session registry: the live set of AI
// sessions producers report, keyed by (source, tool, session), and the
// lifetime rules that drop them when their producer goes quiet.
//
// Every method reaps against the registry's clock before it reads or writes,
// so no caller ever sees a stale session and nothing has to remember to reap.
package sessions

import (
	"sort"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// Policy is how long a session lives without an update. Sessions in done or
// error linger for DoneTTL so the result stays visible; every other state
// (idle, running, waiting) is dropped after StaleAfter.
type Policy struct {
	StaleAfter time.Duration
	DoneTTL    time.Duration
}

// ttl returns the lifetime a session in state gets.
func (p Policy) ttl(state string) time.Duration {
	switch state {
	case "done", "error":
		return p.DoneTTL
	default:
		return p.StaleAfter
	}
}

// Reaped describes a session the registry dropped, and how long it had gone
// without an update.
type Reaped struct {
	Session render.Session
	Age     time.Duration
}

// Registry is safe for concurrent use.
type Registry struct {
	now    func() time.Time
	policy func() Policy
	onReap func(Reaped)

	mu       sync.Mutex // protects sessions
	sessions map[string]render.Session
}

// New returns an empty registry. now is its clock. policy is read on every
// call, so a config hot-reload applies to the next access. onReap (may be nil)
// is called once per dropped session, with the registry locked: it must not
// call back into the registry.
func New(now func() time.Time, policy func() Policy, onReap func(Reaped)) *Registry {
	return &Registry{
		now:      now,
		policy:   policy,
		onReap:   onReap,
		sessions: make(map[string]render.Session),
	}
}

// View is a point-in-time copy of the live sessions.
type View struct {
	Now      time.Time
	Sessions []render.Session // newest UpdatedAt first
}

// Winner is the session the display should lead with (render.PickWinning);
// nil when no session is active.
func (v View) Winner() *render.Session {
	win, _, _ := render.PickWinning(v.Sessions)
	return win
}

// Count is the number of sessions in state.
func (v View) Count(state string) int {
	n := 0
	for _, s := range v.Sessions {
		if s.State == state {
			n++
		}
	}
	return n
}

// Upsert stores s under s.Key(), stamping UpdatedAt with the registry clock,
// and returns the resulting view plus the state that key held before ("" if
// it was new or had been reaped). Reading the prior state and writing happen
// under one lock, so concurrent upserts of one session never misreport the
// transition.
func (r *Registry) Upsert(s render.Session) (View, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.reapLocked()
	prior := r.sessions[s.Key()].State
	s.UpdatedAt = now
	r.sessions[s.Key()] = s
	return r.viewLocked(now), prior
}

// Delete drops the session with key, if present. Deleting a missing key is
// not an error. The delete happens before the reap, so deleting a session
// that has already gone stale is a delete, not a reap.
func (r *Registry) Delete(key string) View {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, key)
	return r.viewLocked(r.reapLocked())
}

// Clear drops every session. Nothing cleared this way counts as reaped.
func (r *Registry) Clear() View {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.sessions)
	return r.viewLocked(r.now())
}

// View returns the live sessions.
func (r *Registry) View() View {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.viewLocked(r.reapLocked())
}

// reapLocked drops every session older than its state's TTL and returns the
// time it measured against.
func (r *Registry) reapLocked() time.Time {
	now := r.now()
	p := r.policy()
	for key, s := range r.sessions {
		age := now.Sub(s.UpdatedAt)
		if age <= p.ttl(s.State) {
			continue
		}
		delete(r.sessions, key)
		if r.onReap != nil {
			r.onReap(Reaped{Session: s, Age: age})
		}
	}
	return now
}

func (r *Registry) viewLocked(now time.Time) View {
	out := make([]render.Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return View{Now: now, Sessions: out}
}
