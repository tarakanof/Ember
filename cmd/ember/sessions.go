package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Upsert writes req into the session map and returns the resulting
// Render plus the state the session held BEFORE this upsert ("" if
// new). priorState is read and updated under a single App.mu acquisition
// so concurrent POSTs for the same session never misclassify the
// transition (a separate priorState+Upsert pair has a TOCTOU window
// that would let request B's Upsert land between request A's priorState
// read and its own Upsert).
func (a *App) Upsert(req StatusRequest) (Render, string) {
	session := req.normalized()
	key := session.Key()
	a.mu.Lock()
	prior := ""
	if existing, ok := a.sessions[key]; ok {
		prior = existing.State
	}
	a.sessions[key] = session
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render, prior
}

func (a *App) Clear() Render {
	a.mu.Lock()
	clear(a.sessions)
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render
}

func (a *App) Delete(key string) Render {
	a.mu.Lock()
	delete(a.sessions, key)
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render
}

func (a *App) Snapshot() Snapshot {
	now := time.Now()
	a.mu.Lock()
	render := a.renderLocked(now)
	sessions := make([]Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.mu.Unlock()

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return Snapshot{
		Now:      now,
		Sessions: sessions,
		Render:   render,
	}
}

func (a *App) renderLocked(now time.Time) Render {
	cfg := a.cfg.Load()
	staleAfter := time.Duration(cfg.Display.StaleSeconds) * time.Second
	doneTTL := time.Duration(cfg.Display.DoneTTLSeconds) * time.Second
	for key, session := range a.sessions {
		age := now.Sub(session.UpdatedAt)
		var reaped bool
		switch session.State {
		case "done", "error":
			reaped = age > doneTTL
		default:
			reaped = age > staleAfter
		}
		if reaped {
			a.metrics.incSessionEvicted()
			a.logger.Warn("session reaped",
				"source", session.Source,
				"tool", session.Tool,
				"session", session.Session,
				"state", session.State,
				"age_seconds", int(age.Seconds()),
			)
			delete(a.sessions, key)
		}
	}

	var waiting, running, errored, done []Session
	for _, session := range a.sessions {
		switch session.State {
		case "waiting":
			waiting = append(waiting, session)
		case "running":
			running = append(running, session)
		case "error":
			errored = append(errored, session)
		case "done":
			done = append(done, session)
		}
		// idle sessions are intentionally not bucketed — they never win a render slot
	}

	sortSessions(waiting)
	sortSessions(running)
	sortSessions(errored)
	sortSessions(done)

	activeTotal := len(waiting) + len(running) + len(errored)

	// Pick winning group by priority.
	var winningGroup []Session
	var color string
	switch {
	case len(waiting) > 0:
		winningGroup = waiting
		color = "#FF3300"
	case len(errored) > 0:
		winningGroup = errored
		color = "#FF3300"
	case len(running) > 0:
		winningGroup = running
		color = "#00A3FF"
	case len(done) > 0:
		winningGroup = done
		color = "#707070"
	default:
		return Render{
			Text:        cfg.Display.IdleText,
			Color:       "#707070",
			ActiveTotal: activeTotal,
		}
	}

	text := perSessionLabel(winningGroup[0])
	if len(winningGroup) >= 2 {
		text = aggregateLabel(len(waiting), len(running), len(errored), len(done))
	}

	return Render{
		Text:        compactText(text),
		Color:       color,
		Waiting:     len(waiting),
		Running:     len(running),
		Errors:      len(errored),
		Done:        len(done),
		ActiveTotal: activeTotal,
		Message:     winningGroup[0].Message,
	}
}

func sortSessions(sessions []Session) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
}

func firstMessage(session Session, fallback string) string {
	if session.Message != "" {
		return session.Message
	}
	return fallback
}

func labelFor(session Session) string {
	switch strings.ToLower(session.Tool) {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	default:
		if session.Tool == "" {
			return "AI"
		}
		return strings.ToUpper(session.Tool[:1]) + session.Tool[1:]
	}
}

func compactText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 80 {
		return text
	}
	return text[:77] + "..."
}

func perSessionLabel(s Session) string {
	switch s.State {
	case "waiting":
		return "WAIT " + firstMessage(s, "approval")
	case "error":
		return "ERR " + labelFor(s) + " " + firstMessage(s, "error")
	case "running":
		return labelFor(s) + " run"
	case "done":
		msg := firstMessage(s, "")
		if msg != "" {
			return labelFor(s) + " done " + msg
		}
		return labelFor(s) + " done"
	default:
		return labelFor(s)
	}
}

func aggregateLabel(waiting, running, errored, done int) string {
	parts := []string{"AI"}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("W%d", waiting))
	}
	if errored > 0 {
		parts = append(parts, fmt.Sprintf("E%d", errored))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("R%d", running))
	}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("D%d", done))
	}
	return strings.Join(parts, " ")
}
