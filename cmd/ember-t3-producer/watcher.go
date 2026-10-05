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
	state        string
	// settledAt is when this watcher saw the thread go running/waiting ->
	// done/error. The run's own completion time can be long past (a hold on
	// background work outlasts the activity window), and done must still show.
	settledAt time.Time
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
		lt := w.live[th.ID]
		if state == "done" || state == "error" {
			changed := th.ChangedAt
			if lt != nil {
				if lt.state == "running" || lt.state == "waiting" {
					lt.settledAt = now
				}
				if lt.settledAt.After(changed) {
					changed = lt.settledAt
				}
			}
			if now.Sub(changed) > w.activityWindow {
				continue
			}
		}
		seen[th.ID] = true
		req := w.buildStatusRequest(th, state, message)
		fp := req.State + "\x00" + req.Message + "\x00" + req.Activity
		if lt == nil {
			lt = &liveThread{}
			w.live[th.ID] = lt
		}
		lt.state = state
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
	req := w.cfg.StatusRequest(toolName, th.ID, state)
	req.Message = message
	if w.cfg.ActivityTrailEnabled {
		req.Activity = producer.Truncate(th.Title, maxActivityRunes)
	}
	return req
}
