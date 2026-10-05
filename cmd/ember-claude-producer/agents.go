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

// `claude agents --json` (Claude Code ≥ 2.1.288) is the documented way to read
// session state from outside Claude Code: per live session it reports
// status busy|waiting|idle, what a waiting session waits for, pid and
// sessionId. The hooks remain the primary source (tool, messages, usage);
// this watcher only corrects what the hooks cannot see (#266):
//   - a wait that ended without a hook: an approved dialog stays "waiting"
//     until the tool finishes, a dialog dismissed with Esc forever;
//   - a turn interrupted with Esc, which fires no Stop and stays "running";
//   - an active session whose owner died, when the list no longer has it
//     (mostly the heartbeat's 10 s owner check gets there first: a killed
//     process can't rewrite its sessions file, so no trigger fires);
//   - a session whose hooks went quiet (#285) while its marker says done:
//     busy promotes it to running "working".
//
// Each call costs ~0.1 s CPU and a ~75 MB transient process, so the CLI runs
// only while a marker is running or waiting, and then only when a file under
// ~/.claude/sessions changes (Claude rewrites its <pid>.json on every status
// change) or agentsFallbackEvery has passed; for a done marker, only when its
// owner's <pid>.json changes to a non-idle status or the statusline shows
// activity after the marker went done (dormantDue). A disagreement is applied only
// after a second call agrees and the marker did not change in between, so a
// hook that is merely late (Stop lands just before the status goes idle)
// never flips the display.

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

// minAgentsVersion is the first Claude Code release this was verified on;
// an older CLI could take "agents" for a prompt.
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
		// Only rows with a live process carry a status.
		if r.SessionID == "" || r.Status == "" {
			continue
		}
		m[r.SessionID] = r
	}
	return m, nil
}

// agentsPollEnabled reads EMBER_CLAUDE_AGENTS_POLL from the environment, then
// producer.env. Default on.
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

// claudeCandidates lists where the claude CLI may be, best first. The
// native installer's ~/.local/bin/claude is what the user runs; the
// LaunchAgent PATH puts /opt/homebrew/bin first, where a stale copy may sit.
// nvm/npm-global installs aren't on a daemon PATH and aren't found.
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

// parseClaudeVersion reads "2.1.289 (Claude Code)".
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

// claudeVersionCache keeps one `claude --version` per binary path and mtime.
var claudeVersionCache = struct {
	sync.Mutex
	m map[string]claudeCLI
}{m: map[string]claudeCLI{}}

var runCLI = runBounded

// resolveClaudeCLI picks the first executable candidate and gates it on
// minAgentsVersion.
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

// runBounded runs bin in its own process group and returns its stdout. The
// timeout kills the whole group, and WaitDelay stops a grandchild that holds
// stdout open from blocking the call past the timeout.
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
		// Reap anything the call left behind (pgid == leader pid).
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

// agentsDoctorLine reports the cross-check for `doctor`.
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

// claudeSessionsDir is where Claude Code keeps one <pid>.json per live
// session. Its layout is internal, so it is only a trigger for the CLI.
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
	// view is body without the statusline's fields: what "the marker did
	// not change between snapshots" compares, since the statusline rewrites
	// the marker on every assistant message of a busy session.
	view []byte
	m    marker
}

// hookView is body with the statusline-owned fields zeroed.
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

// dormantState is a marker state that a busy session contradicts: the
// hooks said the turn ended, so none will say the next one started if they
// went quiet (#285).
func dormantState(s string) bool {
	return s == "done" || s == "idle"
}

// scanClaudeMarkers lists Claude markers in running or waiting (active) and
// in done or idle (dormant).
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

// claudeSessionStatus reads the status field of a ~/.claude/sessions/<pid>.json
// ("" when unreadable). The layout is internal: it only gates whether the
// CLI is asked, never what is written.
func claudeSessionStatus(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var s struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	return s.Status
}

// statuslineAfterDone is how long after a marker went dormant a statusline
// change counts as the session working again; the statusline re-renders for
// a moment after Stop.
const statuslineAfterDone = 5 * time.Second

// dormantDue reports whether a dormant marker's session may have started a
// turn the hooks didn't report: its sessions/<pid>.json changed (Claude
// rewrites it on every status flip) or the statusline wrote new figures
// well after the marker went dormant, and the file doesn't say idle. Each
// change is acted on once, so a quiet session costs no CLI calls.
func (w *agentsWatcher) dormantDue(d activeMarker) bool {
	changed := false
	path := ""
	if d.m.OwnerPID > 0 && w.sessionsDir != "" {
		path = filepath.Join(w.sessionsDir, strconv.Itoa(d.m.OwnerPID)+".json")
		if fi, err := os.Stat(path); err == nil {
			mt := fi.ModTime()
			if prev, seen := w.pidSeen[d.m.OwnerPID]; !seen || !prev.Equal(mt) {
				changed = true
			}
			w.pidSeen[d.m.OwnerPID] = mt
		}
	}
	if sl := d.m.StatuslineChangedMs; sl != w.slSeen[d.sessionID] {
		w.slSeen[d.sessionID] = sl
		if time.UnixMilli(sl).Sub(time.Unix(d.m.StateChangedAt, 0)) >= statuslineAfterDone {
			changed = true
		}
	}
	if !changed {
		return false
	}
	switch path {
	case "":
		return true
	default:
		st := claudeSessionStatus(path)
		return st == "busy" || st == ""
	}
}

// correction is the state a marker should take from one agents snapshot.
type correction struct {
	state, message string
}

// workingMessage is the message of a run the watcher opened: no hook names
// the tool.
const workingMessage = "working"

// correctableWait reports a wait whose end the agents status can tell: a
// permission dialog (PendingPermission survives the permission_prompt
// Notification that follows it) or a wait this watcher opened. Waits that
// come only from a Notification (quota_auto_resume_stale, agent_needs_input
// for another session, MCP elicitation) are left to the hooks.
func correctableWait(m marker) bool {
	return m.PendingPermission != "" || m.AgentsWait
}

// wantFor compares a marker with its session's agents row; ok is false when
// they agree, the row says nothing the hooks don't, or the marker's state
// came from something the status can't speak for.
func wantFor(m marker, row agentRow) (correction, bool) {
	if m.State == "waiting" && !correctableWait(m) {
		return correction{}, false
	}
	if dormantState(m.State) {
		// A busy session whose turn the hooks ended: its hooks went quiet
		// (#285). Idle agrees; a wait is left for a busy snapshot to open
		// the run first.
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
		// Stop kept the session running for a background subagent,
		// workflow, teammate or cloud session that will wake it; the
		// process may well be idle meanwhile.
		if m.BackgroundWake {
			return correction{}, false
		}
		if m.AgentsRun {
			// No Stop will come for a run no hook opened: it simply ended.
			return correction{"done", "done"}, true
		}
		return correction{"done", interruptedMessage}, true
	}
	return correction{}, false
}

// approvedTool is the tool a PermissionRequest message names, or "".
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
	now         func() time.Time
	run         func(context.Context) ([]byte, error)
	stateDir    string
	sessionsDir string
	lastPrint   uint64
	wasActive   bool
	// unlisted holds active sessions the last call didn't list while their
	// owner lived (headless, SDK, nested): the fallback skips a pass that
	// has nothing else to check.
	unlisted     map[string]bool
	lastCall     time.Time
	backoffUntil time.Time
	pending      map[string]pendingCorrection
	// pidSeen and slSeen are the last sessions/<pid>.json mtime and
	// statusline change seen for dormant markers (dormantDue).
	pidSeen map[int]time.Time
	slSeen  map[string]int64
	failLog *producer.FailureLogger
}

func newAgentsWatcher(stateDir, sessionsDir string) *agentsWatcher {
	return &agentsWatcher{
		now:         time.Now,
		run:         runClaudeAgents,
		stateDir:    stateDir,
		sessionsDir: sessionsDir,
		pending:     map[string]pendingCorrection{},
		unlisted:    map[string]bool{},
		pidSeen:     map[int]time.Time{},
		slSeen:      map[string]int64{},
		failLog:     producer.NewFailureLogger(10 * time.Minute),
	}
}

// step runs one watch pass. It reports whether it called the CLI.
func (w *agentsWatcher) step(ctx context.Context, cfg Config, client *Client) bool {
	now := w.now()
	active, dormant := w.scan()
	wake, dormantDue := w.wakeCandidates(dormant)
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
			// A turn just started: the hook that marked it is fresh, so take
			// the current sessions state as the baseline and wait for a change.
			// A turn interrupted within this same 1 s tick is swallowed by the
			// baseline and caught by the fallback instead. A marker that was
			// already stale (daemon restart) is checked now.
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

// wakeCandidates picks the dormant markers this pass checks: those with a
// fresh sign of a turn (due reports any) and those awaiting confirmation.
// Bookkeeping for markers that are gone is dropped.
func (w *agentsWatcher) wakeCandidates(dormant []activeMarker) (wake []activeMarker, due bool) {
	pids := map[int]bool{}
	ids := map[string]bool{}
	for _, d := range dormant {
		pids[d.m.OwnerPID] = true
		ids[d.sessionID] = true
		_, pending := w.pending[d.sessionID]
		if w.dormantDue(d) {
			due = true
			wake = append(wake, d)
		} else if pending {
			wake = append(wake, d)
		}
	}
	for pid := range w.pidSeen {
		if !pids[pid] {
			delete(w.pidSeen, pid)
		}
	}
	for id := range w.slSeen {
		if !ids[id] {
			delete(w.slSeen, id)
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
			// Nested and SDK sessions aren't listed, so absence alone
			// proves nothing: check the owner process.
			if pid, start, ok := markerOwner(a.markerP); ok && !ownerAlive(pid, start) {
				reapMarker(ctx, client, a.markerP, a.lockP)
			} else {
				w.unlisted[a.sessionID] = true
			}
			continue
		}
		want, ok := wantFor(a.m, row)
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

// applyCorrection rewrites the marker only if it still holds what both
// snapshots saw (statusline figures aside, which it keeps), then POSTs
// outside the lock like every other writer.
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
			// A run no hook reported: the old tool trail is not what it
			// does now, and a later idle ends it without "interrupted".
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
			// Like a tool outcome ending the wait: a late permission_prompt
			// Notification for this dialog must not re-enter waiting.
			// An empty ResumedTool matches any prompt (a Notification may
			// have replaced the "approve <tool>" message).
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

// agentsLoop holds the daemon loop's config gate, re-read every
// agentsConfigEvery like the heartbeat's.
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

// agentsWatchLoop runs beside the heartbeat in the daemon.
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
