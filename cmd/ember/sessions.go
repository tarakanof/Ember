package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/sessions"
)

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

// Upsert stores req's session and returns the resulting /state Render plus the state the session held before this upsert ("" if new).
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

func (a *App) legacyRender(v sessions.View) Render {
	waiting, running, errored, done := v.Count("waiting"), v.Count("running"), v.Count("error"), v.Count("done")
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
		Source:      win.Source,
		Tool:        win.Tool,
	}
}

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

func extendsCluster(r rune) bool {
	switch {
	case r == zeroWidthJoiner, r == '︎', r == '️':
		return true
	case r >= 0x1f3fb && r <= 0x1f3ff:
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
