package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/sessions"
)

// newSessionRegistry builds the session registry on clock now, with the
// staleness policy read from the live config on every access, and every reap
// logged and counted.
func (a *App) newSessionRegistry(now func() time.Time) *sessions.Registry {
	return sessions.New(now, a.sessionPolicy, a.onSessionReaped)
}

func (a *App) sessionPolicy() sessions.Policy {
	d := a.cfg.Load().Display
	return sessions.Policy{
		StaleAfter: time.Duration(d.StaleSeconds) * time.Second,
		DoneTTL:    time.Duration(d.DoneTTLSeconds) * time.Second,
	}
}

func (a *App) onSessionReaped(r sessions.Reaped) {
	a.metrics.incSessionEvicted()
	a.logger.Warn("session reaped",
		"source", r.Session.Source,
		"tool", r.Session.Tool,
		"session", r.Session.Session,
		"state", r.Session.State,
		"age_seconds", int(r.Age.Seconds()),
	)
}

// Upsert stores req's session and returns the resulting /state Render plus
// the state the session held before this upsert ("" if new). The registry
// reads the prior state and writes under one lock, so concurrent POSTs for
// the same session never misclassify the transition.
func (a *App) Upsert(req StatusRequest) (Render, string) {
	v, prior := a.sessions.Upsert(req.normalized())
	return a.legacyRender(v), prior
}

func (a *App) Clear() Render {
	return a.legacyRender(a.sessions.Clear())
}

func (a *App) Delete(key string) Render {
	return a.legacyRender(a.sessions.Delete(key))
}

// Snapshot is the GET /state body and the coordinator's view of the sessions.
func (a *App) Snapshot() Snapshot {
	v := a.sessions.View()
	return Snapshot{
		Now:      v.Now,
		Sessions: v.Sessions,
		Render:   a.legacyRender(v),
	}
}

// legacyRender is the /state summary of v: the winner's label (or an
// aggregate when its state group has two or more sessions), colour and
// per-state counters.
func (a *App) legacyRender(v sessions.View) Render {
	waiting, running, errored, done := v.Count("waiting"), v.Count("running"), v.Count("error"), v.Count("done")
	// Done sessions linger for display but no longer count as active.
	activeTotal := waiting + running + errored

	win := v.Winner()
	if win == nil {
		return Render{
			Text:        a.cfg.Load().Display.IdleText,
			Color:       "#707070",
			ActiveTotal: activeTotal,
		}
	}

	text := perSessionLabel(*win)
	if v.Count(win.State) >= 2 {
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
