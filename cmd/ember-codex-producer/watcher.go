package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const keepaliveInterval = 15 * time.Second

type sessionState struct {
	path         string
	uuid         string
	offset       int64
	derived      derived
	lastModified time.Time
	lastPostedAt time.Time
	fingerprint  string
}

type watcher struct {
	cfg            Config
	now            func() time.Time
	activityWindow time.Duration
	sessions       map[string]*sessionState
	ignored        map[string]bool
}

func newWatcher(cfg Config) *watcher {
	return &watcher{
		cfg:            cfg,
		now:            time.Now,
		activityWindow: time.Duration(cfg.ActivityWindowSeconds) * time.Second,
		sessions:       map[string]*sessionState{},
		ignored:        map[string]bool{},
	}
}

func (w *watcher) candidateFiles(now time.Time) []string {
	var out []string
	for _, day := range []time.Time{now.UTC(), now.UTC().AddDate(0, 0, -1)} {
		dir := filepath.Join(w.cfg.SessionsDir, day.Format("2006"), day.Format("01"), day.Format("02"))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "rollout-") || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func buildUsageRequest(d derived) (producer.UsageRequest, bool) {
	if d.weeklyResetAt == 0 {
		return producer.UsageRequest{}, false
	}
	loc := time.Now().Location()
	return producer.UsageRequest{
		Tool:   "codex",
		Source: "codex_stream",
		FiveHour: &producer.UsageWindow{UsedPercent: d.primaryRaw, ResetsAt: d.rateResetAt,
			ResetLabel: time.Unix(d.rateResetAt, 0).In(loc).Format("15:04")},
		SevenDay: &producer.UsageWindow{UsedPercent: d.weeklyRaw, ResetsAt: d.weeklyResetAt,
			ResetLabel: strings.ToUpper(time.Unix(d.weeklyResetAt, 0).In(loc).Format("Mon"))},
	}, true
}

func (w *watcher) tick() (posts []producer.StatusRequest, deletes []producer.DeleteRequest, usages []producer.UsageRequest) {
	now := w.now()
	candidates := map[string]bool{}
	for _, path := range w.candidateFiles(now) {
		candidates[path] = true
	}
	scan := map[string]bool{}
	for path := range candidates {
		scan[path] = true
	}
	for path := range w.sessions {
		scan[path] = true
	}
	gone := map[string]bool{}
	for path := range scan {
		if w.ignored[path] {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				gone[path] = true
			}
			continue
		}
		ss := w.sessions[path]
		if ss == nil {
			meta, ok := readFirstMeta(path)
			if !ok {
				continue
			}
			if meta.source != "cli" {
				w.ignored[path] = true
				continue
			}
			ss = &sessionState{path: path, uuid: meta.id}
			w.sessions[path] = ss
		}
		if info.Size() < ss.offset {
			ss.offset = 0
			ss.derived = derived{}
		}
		if lines, newOffset, err := readNewLines(path, ss.offset); err == nil {
			for _, ln := range lines {
				ss.derived.foldEvent(ln, w.cfg.ContextPctEnabled, w.cfg.RatePctEnabled, w.cfg.ActivityTrailEnabled)
			}
			ss.offset = newOffset
		}
		ss.lastModified = info.ModTime()
		if ss.derived.state == "" {
			continue
		}
		if now.Sub(ss.lastModified) > w.activityWindow {
			continue
		}
		fp := fingerprint(ss.derived)
		if fp != ss.fingerprint || now.Sub(ss.lastPostedAt) >= keepaliveInterval {
			posts = append(posts, buildStatusRequest(w.cfg, ss.uuid, ss.derived))
			if u, ok := buildUsageRequest(ss.derived); ok {
				usages = append(usages, u)
			}
			ss.fingerprint = fp
			ss.lastPostedAt = now
		}
	}
	for path, ss := range w.sessions {
		if gone[path] || now.Sub(ss.lastModified) > w.activityWindow {
			deletes = append(deletes, producer.DeleteRequest{Source: w.cfg.Source, Tool: "codex", Session: ss.uuid})
			delete(w.sessions, path)
		}
	}
	for path := range w.ignored {
		if !candidates[path] {
			delete(w.ignored, path)
		}
	}
	return posts, deletes, usages
}

func readFirstMeta(path string) (sessionMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return sessionMeta{}, false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return sessionMeta{}, false
	}
	return parseSessionMeta(line)
}

func fingerprint(d derived) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s", d.state, d.message, d.activity, ptrStr(d.contextPct), ptrStr(d.rateWindowPct))
}

func ptrStr(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}
