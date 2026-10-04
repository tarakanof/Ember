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
	// viaClaude marks a session Claude Code's Codex plugin started.
	viaClaude bool
}

// fileStamp identifies a file version, so an idle rollout is skipped until it changes.
type fileStamp struct {
	modTime time.Time
	size    int64
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
	// idle caches rollouts found already past the activity window, so they
	// are not re-read every tick; a new mtime or size re-opens them.
	idle map[string]fileStamp
	// usageFP and usagePostedAt dedupe POST /v1/usage, which carries the
	// newest rate-limit snapshot across all sessions.
	usageFP       string
	usagePostedAt time.Time
	// owned are session ids the app-server source covers; the watcher keeps
	// folding their rollouts but posts nothing for them (set before tick).
	owned map[string]bool
	// rateExtra is the app-server's account rate snapshot, a candidate for
	// the newest /v1/usage snapshot (set before tick; nil when absent).
	rateExtra *derived
	// handedOver are ids the watcher had posted when the app-server took
	// them over (reset each tick); cycle DELETEs those it does not post.
	handedOver []string
	// reads counts rollout opens, for tests.
	reads int
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
		idle:           map[string]fileStamp{},
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
// activity window, wherever their day dir is. Codex never prunes the tree, so
// the walk grows with it (about 7 ms for 580 files every 30 s).
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
	w.handedOver = nil
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
			stamp := fileStamp{modTime: info.ModTime(), size: info.Size()}
			if now.Sub(stamp.modTime) > w.activityWindow {
				w.idle[path] = stamp // finished long ago: skip until it changes
				continue
			}
			delete(w.idle, path)
			w.reads++
			meta, ok, complete := readFirstMeta(path)
			if !ok {
				if complete {
					w.ignored[path] = true // a whole first line that is not session_meta
				}
				continue
			}
			if !w.cfg.tracks(meta) {
				w.ignored[path] = true
				continue
			}
			ss = &sessionState{path: path, uuid: meta.id, viaClaude: meta.originator == claudeOriginator}
			w.sessions[path] = ss
		}
		if info.Size() < ss.offset {
			ss.offset = 0
			ss.derived = derived{}
		}
		w.reads++
		if lines, newOffset, err := readNewLines(path, ss.offset); err == nil {
			for _, ln := range lines {
				ss.derived.foldEvent(ln, w.cfg.ContextPctEnabled, w.cfg.RatePctEnabled, w.cfg.ActivityTrailEnabled)
			}
			ss.offset = newOffset
		}
		ss.derived.expireWindows(now)
		ss.lastModified = info.ModTime()
		if w.owned[ss.uuid] {
			if !ss.lastPostedAt.IsZero() {
				w.handedOver = append(w.handedOver, ss.uuid)
			}
			// The app-server source posts this session. Forget the watcher's
			// post so it neither DELETEs it nor waits to post on release.
			ss.lastPostedAt, ss.fingerprint = time.Time{}, ""
			continue
		}
		if ss.derived.state == "" {
			continue
		}
		if now.Sub(ss.lastModified) > w.activityWindow {
			continue
		}
		fp := fingerprint(ss.derived)
		if fp != ss.fingerprint || now.Sub(ss.lastPostedAt) >= keepaliveInterval {
			req := buildStatusRequest(w.cfg, ss.uuid, ss.derived)
			if ss.viaClaude {
				req.Message = viaClaudeMessage(req.Message)
			}
			posts = append(posts, req)
			ss.fingerprint = fp
			ss.lastPostedAt = now
		}
	}
	if u, ok := w.usage(now); ok {
		usages = append(usages, u)
	}
	for path, ss := range w.sessions {
		if gone[path] || now.Sub(ss.lastModified) > w.activityWindow {
			// Only a session this producer posted exists on the server.
			if !ss.lastPostedAt.IsZero() {
				deletes = append(deletes, producer.DeleteRequest{Source: w.cfg.Source, Tool: "codex", Session: ss.uuid})
			}
			delete(w.sessions, path)
		}
	}
	for path := range w.ignored {
		if !candidates[path] {
			delete(w.ignored, path)
		}
	}
	for path := range w.idle {
		if !candidates[path] {
			delete(w.idle, path)
		}
	}
	return posts, deletes, usages
}

// usage returns the newest rate-limit snapshot across live sessions when it
// changed or the keepalive interval passed; an older session's last-seen
// limits never overwrite a newer one's.
func (w *watcher) usage(now time.Time) (producer.UsageRequest, bool) {
	var newest *derived
	for _, ss := range w.sessions {
		d := &ss.derived
		if d.rateResetAt == 0 && d.weeklyResetAt == 0 {
			continue
		}
		if now.Sub(ss.lastModified) > w.activityWindow {
			continue
		}
		if newest == nil || d.rateAt.After(newest.rateAt) {
			newest = d
		}
	}
	if x := w.rateExtra; x != nil && (newest == nil || x.rateAt.After(newest.rateAt)) {
		newest = x
	}
	if newest == nil {
		return producer.UsageRequest{}, false
	}
	u, ok := buildUsageRequest(*newest)
	if !ok {
		return u, false
	}
	fp := fmt.Sprintf("%v|%d|%v|%d", newest.primaryRaw, newest.rateResetAt, newest.weeklyRaw, newest.weeklyResetAt)
	if fp == w.usageFP && now.Sub(w.usagePostedAt) < keepaliveInterval {
		return producer.UsageRequest{}, false
	}
	w.usageFP, w.usagePostedAt = fp, now
	return u, true
}

// posted reports whether the watcher holds a posted session with this id.
func (w *watcher) posted(id string) bool {
	for _, ss := range w.sessions {
		if ss.uuid == id && !ss.lastPostedAt.IsZero() {
			return true
		}
	}
	return false
}

func viaClaudeMessage(msg string) string {
	if msg == "" {
		return "via Claude"
	}
	return truncate("via Claude: "+msg, 80)
}

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
