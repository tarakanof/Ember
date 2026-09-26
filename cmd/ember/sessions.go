package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/render"
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

	sessions := make([]Session, 0, len(a.sessions))
	count := make(map[string]int, 5)
	for _, session := range a.sessions {
		sessions = append(sessions, session)
		count[session.State]++
	}
	waiting, running, errored, done := count["waiting"], count["running"], count["error"], count["done"]
	// Done sessions linger for display but no longer count as active.
	activeTotal := waiting + running + errored

	win, _, _ := render.PickWinning(sessions)
	if win == nil {
		return Render{
			Text:        cfg.Display.IdleText,
			Color:       "#707070",
			ActiveTotal: activeTotal,
		}
	}

	text := perSessionLabel(*win)
	if count[win.State] >= 2 {
		text = aggregateLabel(waiting, running, errored, done)
	}

	return Render{
		Text:        compactText(text),
		Color:       legacyStateColor(win.State),
		Waiting:     waiting,
		Running:     running,
		Errors:      errored,
		Done:        done,
		ActiveTotal: activeTotal,
		Message:     win.Message,
	}
}

// legacyStateColor is the /state Render colour for the winning state. It
// predates the clock palette (render.colorForState), and /state clients
// already see these values, so it stays its own table.
func legacyStateColor(state string) string {
	switch state {
	case "waiting", "error":
		return "#FF3300"
	case "running":
		return "#00A3FF"
	default:
		return "#707070"
	}
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
		first, size := utf8.DecodeRuneInString(session.Tool)
		return string(unicode.ToUpper(first)) + session.Tool[size:]
	}
}

// compactText collapses whitespace and caps the label at 80 characters,
// counted in runes so a Cyrillic or emoji message is never cut mid-sequence
// (which would surface as U+FFFD in /state). The cut also backs off so it
// never strands a combining mark, variation selector, skin-tone modifier or
// ZWJ-joined emoji part: an approximation of a grapheme boundary, since the
// standard library has no segmenter.
func compactText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) <= 80 {
		return text
	}
	cut := 77
	for cut > 0 && (extendsCluster(r[cut]) || r[cut-1] == zeroWidthJoiner) {
		cut--
	}
	return string(r[:cut]) + "..."
}

const zeroWidthJoiner = '‍'

// extendsCluster reports whether r attaches to the rune before it.
func extendsCluster(r rune) bool {
	switch {
	case r == zeroWidthJoiner, r == '︎', r == '️':
		return true
	case r >= 0x1f3fb && r <= 0x1f3ff: // emoji skin-tone modifiers
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me)
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
