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
	resumeScanInterval = 30 * time.Second
)

type sessionState struct {
	path         string
	uuid         string
	offset       int64
	derived      derived
	lastModified time.Time
	post         producer.Repost
	viaClaude    bool
}

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
	loc            *time.Location
	recent         map[string]bool
	lastWalk       time.Time
	idle           map[string]fileStamp
	usagePost      producer.Repost
	owned          map[string]bool
	rateExtra      *derived
	handedOver     []string
	reads          int
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
				w.idle[path] = stamp
				continue
			}
			delete(w.idle, path)
			w.reads++
			meta, ok, complete := readFirstMeta(path)
			if !ok {
				if complete {
					w.ignored[path] = true
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
			if ss.post.Posted() {
				w.handedOver = append(w.handedOver, ss.uuid)
			}
			ss.post.Reset()
			continue
		}
		if ss.derived.state == "" {
			continue
		}
		if now.Sub(ss.lastModified) > w.activityWindow {
			continue
		}
		if ss.post.Due(fingerprint(ss.derived), now) {
			req := buildStatusRequest(w.cfg, ss.uuid, ss.derived)
			if ss.viaClaude {
				req.Message = viaClaudeMessage(req.Message)
			}
			posts = append(posts, req)
		}
	}
	if u, ok := w.usage(now); ok {
		usages = append(usages, u)
	}
	for path, ss := range w.sessions {
		if gone[path] || now.Sub(ss.lastModified) > w.activityWindow {
			if ss.post.Posted() {
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
	if !w.usagePost.Due(fp, now) {
		return producer.UsageRequest{}, false
	}
	return u, true
}

func (w *watcher) posted(id string) bool {
	for _, ss := range w.sessions {
		if ss.uuid == id && ss.post.Posted() {
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
