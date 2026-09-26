package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/render"
)

type StatusRequest struct {
	Source         string  `json:"source"`
	Tool           string  `json:"tool"`
	Session        string  `json:"session"`
	State          string  `json:"state"`
	Message        string  `json:"message"`
	TokensToday    int64   `json:"tokens_today"`
	ContextPct     *int    `json:"context_pct,omitempty"`
	SourceColor    *string `json:"source_color,omitempty"`
	RateWindowPct  *int    `json:"rate_window_pct,omitempty"`
	Activity       string  `json:"activity,omitempty"`
	ContextNumber  bool    `json:"context_number,omitempty"`
	RateBottomBar  bool    `json:"rate_bottom_bar,omitempty"`
	RateResetAt    int64   `json:"rate_reset_at,omitempty"`
	RateReset      bool    `json:"rate_reset,omitempty"`
	RateResetLabel string  `json:"rate_reset_label,omitempty"`
	SourceCard     *bool   `json:"source_card,omitempty"`
	SessionBar     *bool   `json:"session_bar,omitempty"`
}

// normalized is the Session a request describes. UpdatedAt is left zero: the
// session registry stamps it with its own clock on Upsert.
func (r StatusRequest) normalized() Session {
	source := strings.TrimSpace(r.Source)
	if source == "" {
		source = "unknown"
	}
	tool := strings.ToLower(strings.TrimSpace(r.Tool))
	if tool == "" {
		tool = "ai"
	}
	session := strings.TrimSpace(r.Session)
	if session == "" {
		session = "default"
	}

	state := strings.ToLower(strings.TrimSpace(r.State))
	if state == "" {
		state = "idle"
	}
	if !validState(state) {
		state = "idle"
	}

	return Session{
		Source:         source,
		Tool:           tool,
		Session:        session,
		State:          state,
		Message:        strings.TrimSpace(r.Message),
		TokensToday:    r.TokensToday,
		ContextPct:     r.ContextPct,
		SourceColor:    r.SourceColor,
		RateWindowPct:  r.RateWindowPct,
		Activity:       strings.TrimSpace(r.Activity),
		ContextNumber:  r.ContextNumber,
		RateBottomBar:  r.RateBottomBar,
		RateResetAt:    r.RateResetAt,
		RateReset:      r.RateReset,
		RateResetLabel: r.RateResetLabel,
		SourceCard:     r.SourceCard,
		SessionBar:     r.SessionBar,
	}
}

func (r StatusRequest) validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return errors.New("source is required")
	}
	if strings.TrimSpace(r.Tool) == "" {
		return errors.New("tool is required")
	}
	if strings.TrimSpace(r.Session) == "" {
		return errors.New("session is required")
	}
	state := strings.ToLower(strings.TrimSpace(r.State))
	if state == "" {
		return errors.New("state is required")
	}
	if !validState(state) {
		return fmt.Errorf("invalid state %q (must be one of idle, running, waiting, done, error)", state)
	}
	if r.ContextPct != nil {
		if *r.ContextPct < 0 || *r.ContextPct > 100 {
			return fmt.Errorf("context_pct out of range %d (must be 0..100)", *r.ContextPct)
		}
	}
	if r.SourceColor != nil {
		if !isHexColor(*r.SourceColor) {
			return fmt.Errorf("source_color %q must match #RRGGBB hex", *r.SourceColor)
		}
	}
	if r.RateWindowPct != nil {
		if *r.RateWindowPct < 0 || *r.RateWindowPct > 100 {
			return fmt.Errorf("rate_window_pct out of range %d (must be 0..100)", *r.RateWindowPct)
		}
	}
	// Count runes, not bytes: producers truncate activity to 80 runes
	// (internal/producer.Truncate), so a multibyte activity (Cyrillic, emoji)
	// can exceed 80 bytes while still being ≤80 characters. A byte check here
	// 400s the whole status POST for such activity.
	if n := utf8.RuneCountInString(strings.TrimSpace(r.Activity)); n > 80 {
		return fmt.Errorf("activity too long (%d chars, max 80)", n)
	}
	if r.RateResetAt < 0 {
		return fmt.Errorf("rate_reset_at must be a non-negative unix timestamp, got %d", r.RateResetAt)
	}
	return nil
}

func validState(state string) bool {
	switch state {
	case "idle", "running", "waiting", "done", "error":
		return true
	default:
		return false
	}
}

// isHexColor reports whether s is a 7-char string of the form "#RRGGBB"
// with lowercase or uppercase hex digits.
func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	var req StatusRequest
	if !a.decodeOrReject(w, r, &req, false) {
		return
	}
	if err := req.validate(); err != nil {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", validationField(err),
		)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	normalized := req.normalized()
	render, prior := a.Upsert(req)
	a.recordActivityHeartbeat(normalized, time.Now())
	a.coord.Send(coordCmd{
		kind:       cmdUpsert,
		sessionKey: normalized.Key(),
		priorState: prior,
		newState:   normalized.State,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "render": render})
}

func (a *App) handleClear(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "too_large",
		)
		writeError(w, http.StatusRequestEntityTooLarge, err)
		return
	}
	render := a.Clear()
	a.coord.Send(coordCmd{kind: cmdClear})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "render": render})
}

type NotifyRequest struct {
	Text     string `json:"text"`
	Color    string `json:"color"`
	Duration int    `json:"duration"`
	Hold     bool   `json:"hold"`
	// TextCase is NG's textCase ("inherit", "upper", "asTyped"); empty means
	// "upper", Ember's default for every text payload.
	TextCase string `json:"text_case"`
}

type DeleteRequest struct {
	Source  string `json:"source"`
	Tool    string `json:"tool"`
	Session string `json:"session"`
}

func (r DeleteRequest) validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return errors.New("source is required")
	}
	if strings.TrimSpace(r.Tool) == "" {
		return errors.New("tool is required")
	}
	if strings.TrimSpace(r.Session) == "" {
		return errors.New("session is required")
	}
	return nil
}

func (r DeleteRequest) key() string {
	return strings.TrimSpace(r.Source) + "/" + strings.ToLower(strings.TrimSpace(r.Tool)) + "/" + strings.TrimSpace(r.Session)
}

func (a *App) handleDeleteStatus(w http.ResponseWriter, r *http.Request) {
	var req DeleteRequest
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if err := req.validate(); err != nil {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", validationField(err),
		)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.Delete(req.key())
	a.coord.Send(coordCmd{kind: cmdDelete, sessionKey: req.key()})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleNotify(w http.ResponseWriter, r *http.Request) {
	var req NotifyRequest
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if req.Text == "" {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", "text",
		)
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	if req.TextCase != "" && !render.ValidTextCase(req.TextCase) {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", "text_case",
		)
		writeError(w, http.StatusBadRequest, errors.New("text_case must be inherit, upper or asTyped"))
		return
	}
	if req.Color == "" {
		req.Color = "#FFFFFF"
	}
	if req.Duration <= 0 {
		req.Duration = 5
	}
	payload := render.NotifyPayload(req.Text, req.Color, req.TextCase, req.Duration, req.Hold)
	payload["name"] = notifyNameNotify
	if err := a.publisher.Notify(r.Context(), payload); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validationField extracts a field name from validation errors that
// follow the convention "field-name <reason>" (e.g. "source is required").
// Returns the first whitespace-delimited token. Best-effort; falls back
// to the full message if the format doesn't match.
func validationField(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for i, c := range msg {
		if c == ' ' {
			return msg[:i]
		}
	}
	return msg
}
