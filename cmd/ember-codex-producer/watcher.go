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

const (
	keepaliveInterval = 15 * time.Second
	// resumeScanInterval paces the walk of the whole sessions tree that finds
	// resumed sessions: Codex appends them to their original, older day dir.
	resumeScanInterval = 30 * time.Second
)

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
	// loc is the zone Codex names day dirs in (recorder.rs uses local time).
	loc *time.Location
	// recent holds rollouts outside the day-dir scan modified within the
	// activity window, refreshed by a tree walk every resumeScanInterval.
	recent   map[string]bool
	lastWalk time.Time
}

func newWatcher(cfg Config) *watcher {
	return &watcher{
		cfg:            cfg,
		now:            time.Now,
		activityWindow: time.Duration(cfg.ActivityWindowSeconds) * time.Second,
		sessions:       map[string]*sessionState{},
		ignored:        map[string]bool{},
		loc:            time.Local,
		recent:         map[string]bool{},
	}
}

func isRolloutName(name string) bool {
	return strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl")
}

// candidateFiles lists rollouts in the local yesterday/today/tomorrow day dirs
// (tomorrow covers a clock or zone change) plus recently modified ones found
// by the periodic tree walk.
func (w *watcher) candidateFiles(now time.Time) []string {
	if now.Sub(w.lastWalk) >= resumeScanInterval || now.Before(w.lastWalk) {
		w.recent = w.recentFiles(now)
		w.lastWalk = now
	}
	seen := map[string]bool{}
	var out []string
	local := now.In(w.loc)
	for _, off := range []int{0, -1, 1} {
		day := local.AddDate(0, 0, off)
		dir := filepath.Join(w.cfg.SessionsDir, day.Format("2006"), day.Format("01"), day.Format("02"))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !isRolloutName(e.Name()) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			seen[path] = true
			out = append(out, path)
		}
	}
	for path := range w.recent {
		if !seen[path] {
			out = append(out, path)
		}
	}
	return out
}

// recentFiles walks the sessions tree for rollouts modified within the
// activity window, wherever their day dir is.
func (w *watcher) recentFiles(now time.Time) map[string]bool {
	out := map[string]bool{}
	_ = filepath.WalkDir(w.cfg.SessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isRolloutName(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if now.Sub(info.ModTime()) <= w.activityWindow {
			out[path] = true
		}
		return nil
	})
	return out
}

// buildUsageRequest reports the windows seen so far; either may be absent
// (Codex sends null for a window the plan does not have).
func buildUsageRequest(d derived) (producer.UsageRequest, bool) {
	if d.weeklyResetAt == 0 && d.rateResetAt == 0 {
		return producer.UsageRequest{}, false
	}
	loc := time.Now().Location()
	req := producer.UsageRequest{Tool: "codex", Source: "codex_stream"}
	if d.rateResetAt != 0 {
		req.FiveHour = &producer.UsageWindow{UsedPercent: d.primaryRaw, ResetsAt: d.rateResetAt,
			ResetLabel: time.Unix(d.rateResetAt, 0).In(loc).Format("15:04")}
	}
	if d.weeklyResetAt != 0 {
		req.SevenDay = &producer.UsageWindow{UsedPercent: d.weeklyRaw, ResetsAt: d.weeklyResetAt,
			ResetLabel: strings.ToUpper(time.Unix(d.weeklyResetAt, 0).In(loc).Format("Mon"))}
	}
	return req, true
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
			meta, ok, complete := readFirstMeta(path)
			if !ok {
				if complete {
					w.ignored[path] = true // a whole first line that is not session_meta
				}
				continue
			}
			if !trackedSource(meta.source) {
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

// readFirstMeta parses a rollout's first line; complete reports whether that
// line was fully written, so a parse failure is final rather than a race.
func readFirstMeta(path string) (meta sessionMeta, ok, complete bool) {
	f, err := os.Open(path)
	if err != nil {
		return sessionMeta{}, false, false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	complete = err == nil
	if !complete {
		return sessionMeta{}, false, false
	}
	meta, ok = parseSessionMeta(line)
	return meta, ok, complete
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
