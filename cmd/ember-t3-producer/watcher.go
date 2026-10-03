package main

import (
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

// keepaliveInterval re-posts unchanged threads to stay under the server's staleness reap (same as Codex).
const keepaliveInterval = 15 * time.Second

type liveThread struct {
	fingerprint  string
	lastPostedAt time.Time
}

// watcher diffs successive thread snapshots into status POSTs and DELETEs.
type watcher struct {
	cfg            Config
	activityWindow time.Duration
	live           map[string]*liveThread
}

func newWatcher(cfg Config) *watcher {
	return &watcher{
		cfg:            cfg,
		activityWindow: time.Duration(cfg.ActivityWindowSeconds) * time.Second,
		live:           map[string]*liveThread{},
	}
}

// tick reports threads that are running or waiting, plus done/error threads
// that changed within the activity window; every other previously reported
// thread is deleted. Pass nil threads when the T3 server is not running.
func (w *watcher) tick(threads []thread, now time.Time) (posts []producer.StatusRequest, deletes []producer.DeleteRequest) {
	seen := map[string]bool{}
	for _, th := range threads {
		state, message, ok := mapThread(th)
		if !ok {
			continue
		}
		if (state == "done" || state == "error") && now.Sub(th.ChangedAt) > w.activityWindow {
			continue
		}
		seen[th.ID] = true
		req := w.buildStatusRequest(th, state, message)
		fp := req.State + "\x00" + req.Message + "\x00" + req.Activity
		lt := w.live[th.ID]
		if lt == nil {
			lt = &liveThread{}
			w.live[th.ID] = lt
		}
		if fp != lt.fingerprint || now.Sub(lt.lastPostedAt) >= keepaliveInterval {
			posts = append(posts, req)
			lt.fingerprint, lt.lastPostedAt = fp, now
		}
	}
	for id := range w.live {
		if !seen[id] {
			deletes = append(deletes, w.deleteRequest(id))
			delete(w.live, id)
		}
	}
	return posts, deletes
}

// dropAll forgets every reported thread and returns their DELETEs.
func (w *watcher) dropAll() []producer.DeleteRequest {
	var out []producer.DeleteRequest
	for id := range w.live {
		out = append(out, w.deleteRequest(id))
		delete(w.live, id)
	}
	return out
}

func (w *watcher) deleteRequest(id string) producer.DeleteRequest {
	return producer.DeleteRequest{Source: w.cfg.Source, Tool: toolName, Session: id}
}

func (w *watcher) buildStatusRequest(th thread, state, message string) producer.StatusRequest {
	req := producer.StatusRequest{
		Source:  w.cfg.Source,
		Tool:    toolName,
		Session: th.ID,
		State:   state,
		Message: message,
	}
	if w.cfg.ActivityTrailEnabled {
		req.Activity = producer.Truncate(th.Title, maxActivityRunes)
	}
	if w.cfg.SourceColor != "" {
		sc := w.cfg.SourceColor
		req.SourceColor = &sc
	}
	sc, sb := w.cfg.SourceCardEnabled, w.cfg.SessionBarEnabled
	req.SourceCard, req.SessionBar = &sc, &sb
	return req
}
