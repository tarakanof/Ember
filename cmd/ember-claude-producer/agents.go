package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const (
	agentsWatchEvery    = time.Second
	agentsFallbackEvery = 60 * time.Second
	agentsConfirmAfter  = 1500 * time.Millisecond
	agentsCallTimeout   = 5 * time.Second
	agentsFailBackoff   = 5 * time.Minute
	agentsConfigEvery   = 10 * time.Second
	agentsKillGrace     = time.Second
	interruptedMessage  = "interrupted"
)

// An older CLI would take "agents" as a prompt: see ARCHITECTURE (Claude producer).
var minAgentsVersion = [3]int{2, 1, 288}

type agentRow struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
}

func parseAgents(out []byte) (map[string]agentRow, error) {
	var rows []agentRow
	if err := json.Unmarshal(bytes.TrimSpace(out), &rows); err != nil {
		return nil, err
	}
	m := make(map[string]agentRow, len(rows))
	for _, r := range rows {
		if r.SessionID == "" || r.Status == "" {
			continue
		}
		m[r.SessionID] = r
	}
	return m, nil
}

func agentsPollEnabled() bool {
	v, ok := os.LookupEnv("EMBER_CLAUDE_AGENTS_POLL")
	if !ok {
		if path, err := envFilePath(); err == nil {
			if data, err := producer.ReadEnvFile(path); err == nil {
				v = data["EMBER_CLAUDE_AGENTS_POLL"]
			}
		}
	}
	return producer.Bool(strings.TrimSpace(v), true)
}

func claudeCandidates() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".local", "bin", "claude"))
	}
	if p, err := exec.LookPath("claude"); err == nil {
		out = append(out, p)
	}
	return append(out, "/opt/homebrew/bin/claude", "/usr/local/bin/claude")
}

var claudeVersionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func parseClaudeVersion(out string) ([3]int, bool) {
	m := claudeVersionRe.FindStringSubmatch(out)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func versionAtLeast(v, min [3]int) bool {
	for i := range v {
		if v[i] != min[i] {
			return v[i] > min[i]
		}
	}
	return true
}

type claudeCLI struct {
	path    string
	version string
	ok      bool
	reason  string
}

var claudeVersionCache = struct {
	sync.Mutex
	m map[string]claudeCLI
}{m: map[string]claudeCLI{}}

var runCLI = runBounded

func resolveClaudeCLI(ctx context.Context) claudeCLI {
	for _, p := range claudeCandidates() {
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			continue
		}
		key := p + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
		claudeVersionCache.Lock()
		c, hit := claudeVersionCache.m[key]
		claudeVersionCache.Unlock()
		if hit {
			return c
		}
		c = claudeCLI{path: p}
		out, err := runCLI(ctx, agentsCallTimeout, p, "--version")
		v, parsed := parseClaudeVersion(string(out))
		switch {
		case err != nil || !parsed:
			c.reason = "claude --version failed"
		case !versionAtLeast(v, minAgentsVersion):
			c.version = fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
			c.reason = fmt.Sprintf("claude %s is older than %d.%d.%d", c.version, minAgentsVersion[0], minAgentsVersion[1], minAgentsVersion[2])
		default:
			c.version = fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
			c.ok = true
		}
		claudeVersionCache.Lock()
		claudeVersionCache.m[key] = c
		claudeVersionCache.Unlock()
		return c
	}
	return claudeCLI{reason: "no claude CLI in ~/.local/bin, PATH, /opt/homebrew/bin or /usr/local/bin"}
}

func runBounded(ctx context.Context, timeout time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = agentsKillGrace
	out, err := cmd.Output()
	if err != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return out, err
}

var runClaudeAgents = func(ctx context.Context) ([]byte, error) {
	c := resolveClaudeCLI(ctx)
	if !c.ok {
		return nil, errors.New(c.reason)
	}
	return runCLI(ctx, agentsCallTimeout, c.path, "agents", "--json")
}

func agentsDoctorLine(ctx context.Context) string {
	if !agentsPollEnabled() {
		return "off (EMBER_CLAUDE_AGENTS_POLL)"
	}
	c := resolveClaudeCLI(ctx)
	if !c.ok {
		return "on, but inactive: " + c.reason
	}
	return fmt.Sprintf("on (claude %s at %s)", c.version, c.path)
}

func claudeSessionsDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

func dirFingerprint(dir string) uint64 {
	h := fnv.New64a()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		h.Write([]byte(e.Name()))
		var b [16]byte
		n := info.ModTime().UnixNano()
		s := info.Size()
		for i := 0; i < 8; i++ {
			b[i] = byte(n >> (8 * i))
			b[8+i] = byte(s >> (8 * i))
		}
		h.Write(b[:])
	}
	return h.Sum64()
}

type activeMarker struct {
	sessionID      string
	markerP, lockP string
	body           []byte
	view           []byte
	m              marker
}

func hookView(body []byte) ([]byte, marker, bool) {
	var m marker
	if json.Unmarshal(body, &m) != nil {
		return nil, m, false
	}
	v := m
	v.RateWindowPct, v.ContextPct, v.RateWeekPct = nil, nil, nil
	v.RateResetAt, v.RateWeekResetAt = 0, 0
	v.RateResetLabel, v.RateWeekResetLabel = "", ""
	v.StatuslineChangedMs, v.StatuslineAt = 0, 0
	b, err := json.Marshal(v)
	if err != nil {
		return nil, m, false
	}
	return b, m, true
}

func dormantState(s string) bool {
	return s == "done" || s == "idle"
}

func scanClaudeMarkers(dir string) (active, dormant []activeMarker) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		p := filepath.Join(dir, name)
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		view, m, ok := hookView(body)
		if !ok || (m.Tool != "" && m.Tool != "claude") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		a := activeMarker{sessionID: id, markerP: p, lockP: lockPath(dir, id), body: body, view: view, m: m}
		switch {
		case m.State == "running" || m.State == "waiting":
			active = append(active, a)
		case dormantState(m.State):
			dormant = append(dormant, a)
		}
	}
	return active, dormant
}

type claudeSession struct {
	Status string `json:"status"`
	// unix ms
	StatusUpdatedAt int64 `json:"statusUpdatedAt"`
}

func readClaudeSession(path string) (claudeSession, bool) {
	var s claudeSession
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &s) != nil {
		return claudeSession{}, false
	}
	return s, true
}

func (w *agentsWatcher) claudeSessionPath(m marker) string {
	if m.OwnerPID <= 0 || w.sessionsDir == "" {
		return ""
	}
	return filepath.Join(w.sessionsDir, strconv.Itoa(m.OwnerPID)+".json")
}

const statuslineAfterDone = 5 * time.Second

func busyAfterDormant(m marker, s claudeSession) bool {
	if s.Status != "busy" {
		return false
	}
	if s.StatusUpdatedAt > 0 {
		return s.StatusUpdatedAt >= (m.StateChangedAt+1)*1000
	}
	return m.StatuslineChangedMs >= m.StateChangedAt*1000+statuslineAfterDone.Milliseconds()
}

func seenKey(m marker, sessionID string) string {
	return strconv.Itoa(m.OwnerPID) + "/" + sessionID
}

func (w *agentsWatcher) noteSessionFile(a activeMarker) (path string, changed bool) {
	path = w.claudeSessionPath(a.m)
	if path == "" {
		return "", false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	k := seenKey(a.m, a.sessionID)
	prev, seen := w.fileSeen[k]
	w.fileSeen[k] = fi.ModTime()
	return path, !seen || !prev.Equal(fi.ModTime())
}

func (w *agentsWatcher) dormantDue(d activeMarker) bool {
	path, changed := w.noteSessionFile(d)
	if path == "" {
		return false
	}
	k := seenKey(d.m, d.sessionID)
	if sl := d.m.StatuslineChangedMs; sl != w.slSeen[k] {
		w.slSeen[k] = sl
		changed = true
	}
	if !changed {
		return false
	}
	s, ok := readClaudeSession(path)
	return ok && busyAfterDormant(d.m, s)
}

type correction struct {
	state, message string
}

const workingMessage = "working"

func correctableWait(m marker) bool {
	return m.PendingPermission != "" || m.AgentsWait
}

func wantFor(m marker, row agentRow) (correction, bool) {
	if m.State == "waiting" && !correctableWait(m) {
		return correction{}, false
	}
	if dormantState(m.State) {
		if row.Status == "busy" {
			return correction{"running", workingMessage}, true
		}
		return correction{}, false
	}
	switch row.Status {
	case "busy":
		if m.State == "waiting" {
			return correction{"running", pickFirstNonEmpty(approvedTool(m.Message), "resumed")}, true
		}
	case "waiting":
		if m.State == "running" {
			return correction{"waiting", pickFirstNonEmpty(row.WaitingFor, "waiting")}, true
		}
	case "idle":
		if m.BackgroundWake {
			return correction{}, false
		}
		if m.AgentsRun {
			return correction{"done", "done"}, true
		}
		return correction{"done", interruptedMessage}, true
	}
	return correction{}, false
}

func (w *agentsWatcher) wantForSession(a activeMarker, row agentRow) (correction, bool) {
	want, ok := wantFor(a.m, row)
	var s claudeSession
	read := false
	if path := w.claudeSessionPath(a.m); path != "" {
		s, read = readClaudeSession(path)
	}
	if ok && dormantState(a.m.State) && (!read || !busyAfterDormant(a.m, s)) {
		return correction{}, false
	}
	if !ok && a.m.AgentsRun && a.m.State == "running" && row.Status == "busy" &&
		read && s.Status != "" && s.Status != "busy" && s.Status != "waiting" {
		return correction{"done", "done"}, true
	}
	return want, ok
}

func approvedTool(msg string) string {
	if t, ok := strings.CutPrefix(msg, "approve "); ok {
		return t
	}
	return ""
}

type pendingCorrection struct {
	want correction
	body []byte
	seen time.Time
}

type agentsWatcher struct {
	now          func() time.Time
	run          func(context.Context) ([]byte, error)
	stateDir     string
	sessionsDir  string
	lastPrint    uint64
	wasActive    bool
	unlisted     map[string]bool
	lastCall     time.Time
	backoffUntil time.Time
	pending      map[string]pendingCorrection
	fileSeen     map[string]time.Time
	slSeen       map[string]int64
	failLog      *producer.FailureLogger
}

func newAgentsWatcher(stateDir, sessionsDir string) *agentsWatcher {
	return &agentsWatcher{
		now:         time.Now,
		run:         runClaudeAgents,
		stateDir:    stateDir,
		sessionsDir: sessionsDir,
		pending:     map[string]pendingCorrection{},
		unlisted:    map[string]bool{},
		fileSeen:    map[string]time.Time{},
		slSeen:      map[string]int64{},
		failLog:     producer.NewFailureLogger(10 * time.Minute),
	}
}

func (w *agentsWatcher) step(ctx context.Context, cfg Config, client *Client) bool {
	now := w.now()
	active, dormant := w.scan()
	wake, dormantDue := w.wakeCandidates(active, dormant)
	if len(active) == 0 && len(wake) == 0 {
		w.wasActive = false
		clear(w.pending)
		return false
	}
	changed := false
	if len(active) == 0 {
		w.wasActive = false
	} else {
		fp := dirFingerprint(w.sessionsDir)
		changed = fp != w.lastPrint
		w.lastPrint = fp
		if !w.wasActive {
			w.wasActive = true
			changed = false
			stale := false
			for _, a := range active {
				if a.m.StateChangedAt != 0 && now.Sub(time.Unix(a.m.StateChangedAt, 0)) >= agentsFallbackEvery {
					stale = true
				}
			}
			if stale {
				w.lastCall = time.Time{}
			} else {
				w.lastCall = now
			}
		}
	}
	if now.Before(w.backoffUntil) {
		return false
	}
	confirmDue := false
	for _, p := range w.pending {
		if now.Sub(p.seen) >= agentsConfirmAfter {
			confirmDue = true
		}
	}
	fallbackDue := len(active) > 0 && now.Sub(w.lastCall) >= agentsFallbackEvery
	if fallbackDue && !changed && !confirmDue && !dormantDue {
		onlyUnlisted := true
		for _, a := range active {
			if !w.unlisted[a.sessionID] {
				onlyUnlisted = false
			}
		}
		fallbackDue = !onlyUnlisted
	}
	if !changed && !confirmDue && !fallbackDue && !dormantDue {
		return false
	}
	w.lastCall = now
	out, err := w.run(ctx)
	if err == nil {
		var rows map[string]agentRow
		if rows, err = parseAgents(out); err == nil {
			w.reconcile(ctx, cfg, client, append(active, wake...), rows, now)
			return true
		}
	}
	w.backoffUntil = now.Add(agentsFailBackoff)
	clear(w.pending)
	w.failLog.Warn(slog.Default(), "claude_agents", "claude agents --json failed; retrying in 5 min", "err", err)
	return true
}

func (w *agentsWatcher) scan() (active, dormant []activeMarker) {
	return scanClaudeMarkers(w.stateDir)
}

func (w *agentsWatcher) wakeCandidates(active, dormant []activeMarker) (wake []activeMarker, due bool) {
	keys := map[string]bool{}
	for _, a := range active {
		keys[seenKey(a.m, a.sessionID)] = true
		w.noteSessionFile(a)
	}
	for _, d := range dormant {
		keys[seenKey(d.m, d.sessionID)] = true
		_, pending := w.pending[d.sessionID]
		if w.dormantDue(d) {
			due = true
			wake = append(wake, d)
		} else if pending {
			wake = append(wake, d)
		}
	}
	for k := range w.fileSeen {
		if !keys[k] {
			delete(w.fileSeen, k)
		}
	}
	for k := range w.slSeen {
		if !keys[k] {
			delete(w.slSeen, k)
		}
	}
	return wake, due
}

func (w *agentsWatcher) reconcile(ctx context.Context, cfg Config, client *Client, active []activeMarker, rows map[string]agentRow, now time.Time) {
	next := map[string]pendingCorrection{}
	clear(w.unlisted)
	for _, a := range active {
		row, listed := rows[a.sessionID]
		if !listed {
			if pid, start, ok := markerOwner(a.markerP); ok && !ownerAlive(pid, start) {
				reapMarker(ctx, client, a.markerP, a.lockP)
			} else {
				w.unlisted[a.sessionID] = true
			}
			continue
		}
		want, ok := w.wantForSession(a, row)
		if !ok {
			continue
		}
		prev, had := w.pending[a.sessionID]
		same := had && prev.want == want && bytes.Equal(prev.body, a.view)
		if same && now.Sub(prev.seen) >= agentsConfirmAfter {
			applyCorrection(ctx, cfg, client, a, want)
			continue
		}
		if same {
			next[a.sessionID] = prev
			continue
		}
		next[a.sessionID] = pendingCorrection{want: want, body: a.view, seen: now}
	}
	w.pending = next
}

func applyCorrection(ctx context.Context, cfg Config, client *Client, a activeMarker, want correction) {
	var body []byte
	from := a.m.State
	_ = withLockExWait(a.lockP, hookLockWait(cfg), func() error {
		cur, err := readMarker(a.markerP)
		if err != nil {
			return nil
		}
		view, m, ok := hookView(cur)
		if !ok || !bytes.Equal(view, a.view) {
			return nil
		}
		if dormantState(m.State) && want.state == "running" {
			m.AgentsRun = true
			m.BackgroundWake = false
			if cfg.ActivityDetailEnabled {
				m.Activity = workingMessage
			} else {
				m.Activity = ""
			}
		}
		if want.state == "done" {
			m.AgentsRun = false
		}
		if m.State == "waiting" && want.state != "waiting" {
			m.ResumedTool = approvedTool(m.Message)
			m.ResumedAt = hookNow().Unix()
		}
		m.State = want.state
		m.Message = truncate(want.message, 80)
		m.StateChangedAt = hookNow().Unix()
		m.AgentsWait = want.state == "waiting"
		if want.state != "waiting" {
			m.PendingPermission, m.PendingToolUseID = "", ""
		}
		b, err := json.Marshal(m)
		if err != nil || writeMarker(a.markerP, b) != nil {
			return nil
		}
		body = b
		return nil
	})
	if body != nil {
		slog.Info("claude agents corrected session", "session", a.sessionID, "from", from, "to", want.state)
		if dormantState(from) {
			slog.Warn("claude session is busy but its hooks are silent; restart it (or run /reload-plugins in it) after installing or updating the ember plugin",
				"session", a.sessionID, "pid", a.m.OwnerPID)
		}
		if err := postReconciled(ctx, cfg, client, a.markerP, a.lockP, body, hookLockWait(cfg)); err != nil {
			tickFailLog.Warn(slog.Default(), "claude_post", "status POST failed", "err", err)
		}
	}
}

type agentsLoop struct {
	w       *agentsWatcher
	enabled func() bool
	load    func() (Config, error)
	cfg     Config
	ok      bool
	cfgAt   time.Time
}

func (l *agentsLoop) pass(ctx context.Context) bool {
	now := l.w.now()
	if l.cfgAt.IsZero() || now.Sub(l.cfgAt) >= agentsConfigEvery {
		l.cfgAt = now
		c, err := l.load()
		l.cfg = c
		l.ok = l.enabled() && err == nil && c.Source != "" && c.ServerURL != ""
	}
	if !l.ok {
		return false
	}
	return l.w.step(ctx, l.cfg, NewDaemonClient(l.cfg))
}

func agentsWatchLoop(ctx context.Context) {
	dir, err := stateDir()
	if err != nil {
		return
	}
	l := &agentsLoop{w: newAgentsWatcher(dir, claudeSessionsDir()), enabled: agentsPollEnabled, load: loadDaemonConfig}
	ticker := time.NewTicker(agentsWatchEvery)
	defer ticker.Stop()
	for {
		l.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

const hooksStaleAfter = 10 * time.Minute

type staleHookSession struct {
	sessionID string
	pid       int
	hookAge   time.Duration
}

func staleHookSessions(markers []marker, rows map[string]agentRow, now time.Time) []staleHookSession {
	var out []staleHookSession
	for _, m := range markers {
		row, ok := rows[m.Session]
		if !ok || row.Status != "busy" {
			continue
		}
		hookAt := m.HookAt
		slAt := max(m.StatuslineAt, m.StatuslineChangedMs/1000)
		if hookAt == 0 || slAt == 0 {
			continue
		}
		hook, sl := time.Unix(hookAt, 0), time.Unix(slAt, 0)
		if now.Sub(sl) > hooksStaleAfter || sl.Sub(hook) < hooksStaleAfter {
			continue
		}
		out = append(out, staleHookSession{sessionID: m.Session, pid: row.PID, hookAge: now.Sub(hook).Truncate(time.Minute)})
	}
	return out
}

func staleHooksDoctorLines(ctx context.Context, stateDir string) []string {
	if !agentsPollEnabled() || !resolveClaudeCLI(ctx).ok {
		return nil
	}
	active, dormant := scanClaudeMarkers(stateDir)
	var markers []marker
	for _, a := range append(active, dormant...) {
		markers = append(markers, a.m)
	}
	if len(markers) == 0 {
		return nil
	}
	out, err := runClaudeAgents(ctx)
	if err != nil {
		return nil
	}
	rows, err := parseAgents(out)
	if err != nil {
		return nil
	}
	var lines []string
	for _, s := range staleHookSessions(markers, rows, time.Now()) {
		lines = append(lines, fmt.Sprintf("WARNING: session %s (pid %d) is busy and its status line runs, but no hook has reported for %s",
			s.sessionID, s.pid, s.hookAge))
	}
	if len(lines) > 0 {
		lines = append(lines, "hint: hooks look stale; restart Claude Code sessions after installing or updating the ember plugin (or run /reload-plugins in them). "+
			"A single tool running over 10 min with statusLine.refreshInterval set looks the same; ignore it then")
	}
	return lines
}
