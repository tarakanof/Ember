package sessions

import (
	"sort"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

type Policy struct {
	StaleAfter time.Duration
	DoneTTL    time.Duration
}

func (p Policy) ttl(state string) time.Duration {
	switch state {
	case "done", "error":
		return p.DoneTTL
	default:
		return p.StaleAfter
	}
}

type Reaped struct {
	Session render.Session
	Age     time.Duration
}

type Registry struct {
	now    func() time.Time
	policy func() Policy
	onReap func(Reaped)

	mu       sync.Mutex
	sessions map[string]render.Session
}

func New(now func() time.Time, policy func() Policy, onReap func(Reaped)) *Registry {
	return &Registry{
		now:      now,
		policy:   policy,
		onReap:   onReap,
		sessions: make(map[string]render.Session),
	}
}

type View struct {
	Now      time.Time
	Sessions []render.Session
}

func (v View) Winner() *render.Session {
	win, _, _ := render.PickWinning(v.Sessions)
	return win
}

func (v View) Count(state string) int {
	n := 0
	for _, s := range v.Sessions {
		if s.State == state {
			n++
		}
	}
	return n
}

func (r *Registry) Upsert(s render.Session) (View, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.reapLocked()
	prior := r.sessions[s.Key()].State
	s.UpdatedAt = now
	r.sessions[s.Key()] = s
	return r.viewLocked(now), prior
}

func (r *Registry) Delete(key string) View {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, key)
	return r.viewLocked(r.reapLocked())
}

func (r *Registry) Clear() View {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.sessions)
	return r.viewLocked(r.now())
}

func (r *Registry) View() View {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.viewLocked(r.reapLocked())
}

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
