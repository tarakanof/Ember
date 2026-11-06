package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/workday"
)

const (
	dayRepliesMax    = 500
	dayReplyFutureOK = 10 * time.Minute
)

type dayReplyIn struct {
	At      int64  `json:"at"`
	Source  string `json:"source"`
	Tool    string `json:"tool"`
	Session string `json:"session"`
	Repo    string `json:"repo,omitempty"`
}

func (r dayReplyIn) validate(now time.Time) error {
	switch {
	case r.At <= 0:
		return errors.New("at must be a unix timestamp in seconds")
	case time.Unix(r.At, 0).After(now.Add(dayReplyFutureOK)):
		return fmt.Errorf("at %d is in the future", r.At)
	case strings.TrimSpace(r.Source) == "":
		return errors.New("source is required")
	case strings.TrimSpace(r.Tool) == "":
		return errors.New("tool is required")
	case strings.TrimSpace(r.Session) == "":
		return errors.New("session is required")
	}
	return nil
}

func (r dayReplyIn) reply() workday.Reply {
	source := strings.TrimSpace(r.Source)
	tool := strings.ToLower(strings.TrimSpace(r.Tool))
	session := strings.TrimSpace(r.Session)
	return workday.Reply{
		At: time.Unix(r.At, 0), Source: source, Tool: tool,
		SessionKey: source + "/" + tool + "/" + session,
		Repo:       normalizedProject(r.Repo), Exact: true,
	}
}

func (a *App) handleDayReplies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Replies []dayReplyIn `json:"replies"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	reject := func(field string, err error) {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr, "path", r.URL.Path, "reason", "validation", "field", field)
		writeError(w, http.StatusBadRequest, err)
	}
	if len(req.Replies) > dayRepliesMax {
		reject("replies", fmt.Errorf("replies: at most %d per request, got %d", dayRepliesMax, len(req.Replies)))
		return
	}
	now := a.dayNow()
	for _, in := range req.Replies {
		if err := in.validate(now); err != nil {
			reject(validationField(err), err)
			return
		}
		if !a.allowSessionKey(w, r, in.Source, in.Tool, in.Session) {
			return
		}
	}
	if !a.dayOn() {
		writeError(w, http.StatusNotFound, errDayDisabled)
		return
	}
	cutoff := now.Add(-dayRetention)
	rs := make([]workday.Reply, 0, len(req.Replies))
	for _, in := range req.Replies {
		if rep := in.reply(); !rep.At.Before(cutoff) {
			rs = append(rs, rep)
		}
	}
	if err := a.recordExactReplies(rs, now); err != nil {
		a.logger.Warn("day replies record failed", "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("day replies write failed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "accepted": len(rs)})
}
