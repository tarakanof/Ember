package main

import (
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type liveThread struct {
	post  producer.Repost
	state string
	// Not the run's completion time: a background hold outlasts the activity window and done must still show.
	settledAt time.Time
}

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
		if lt.post.Due(fp, now) {
			posts = append(posts, req)
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
