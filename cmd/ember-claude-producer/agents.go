package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
//   - a session whose process died, reaped sooner than the heartbeat.
//
// Each call costs ~80 ms CPU and a ~75 MB transient process, so the CLI runs
// only while a marker is running or waiting, and then only when a file under
// ~/.claude/sessions changes (Claude rewrites its <pid>.json on every status
// change) or agentsFallbackEvery has passed. A disagreement is applied only
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
	interruptedMessage  = "interrupted"
)

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
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// claudeBinary finds the claude CLI; the LaunchAgent's PATH is minimal.
func claudeBinary() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "claude"),
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

var runClaudeAgents = func(ctx context.Context) ([]byte, error) {
	bin := claudeBinary()
	if bin == "" {
		return nil, errors.New("claude CLI not found")
	}
	ctx, cancel := context.WithTimeout(ctx, agentsCallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "agents", "--json")
	cmd.Stdin = nil
	return cmd.Output()
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
	m              marker
}

// activeClaudeMarkers lists markers in running or waiting: the only states
// this watcher corrects.
func activeClaudeMarkers(dir string) []activeMarker {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []activeMarker
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
		var m marker
		if json.Unmarshal(body, &m) != nil || (m.Tool != "" && m.Tool != "claude") {
			continue
		}
		if m.State != "running" && m.State != "waiting" {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		out = append(out, activeMarker{sessionID: id, markerP: p, lockP: lockPath(dir, id), body: body, m: m})
	}
	return out
}

// correction is the state a marker should take from one agents snapshot.
type correction struct {
	state, message string
}

// wantFor compares a marker with its session's agents row; ok is false when
// they agree or the row says nothing the hooks don't.
func wantFor(m marker, row agentRow) (correction, bool) {
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
	now          func() time.Time
	run          func(context.Context) ([]byte, error)
	stateDir     string
	sessionsDir  string
	lastPrint    uint64
	wasActive    bool
	lastCall     time.Time
	backoffUntil time.Time
	pending      map[string]pendingCorrection
	failLog      *producer.FailureLogger
}

func newAgentsWatcher(stateDir, sessionsDir string) *agentsWatcher {
	return &agentsWatcher{
		now:         time.Now,
		run:         runClaudeAgents,
		stateDir:    stateDir,
		sessionsDir: sessionsDir,
		pending:     map[string]pendingCorrection{},
		failLog:     producer.NewFailureLogger(10 * time.Minute),
	}
}

// step runs one watch pass. It reports whether it called the CLI.
func (w *agentsWatcher) step(ctx context.Context, cfg Config, client *Client) bool {
	now := w.now()
	active := activeClaudeMarkers(w.stateDir)
	if len(active) == 0 {
		w.wasActive = false
		clear(w.pending)
		return false
	}
	fp := dirFingerprint(w.sessionsDir)
	changed := fp != w.lastPrint
	w.lastPrint = fp
	if !w.wasActive {
		// A turn just started: the hook that marked it is fresh, so take
		// the current sessions state as the baseline and wait for a change.
		w.wasActive = true
		changed = false
		if w.lastCall.IsZero() || now.Sub(w.lastCall) >= agentsFallbackEvery {
			w.lastCall = now
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
	if !changed && !confirmDue && now.Sub(w.lastCall) < agentsFallbackEvery {
		return false
	}
	w.lastCall = now
	out, err := w.run(ctx)
	if err == nil {
		var rows map[string]agentRow
		if rows, err = parseAgents(out); err == nil {
			w.reconcile(ctx, cfg, client, active, rows, now)
			return true
		}
	}
	w.backoffUntil = now.Add(agentsFailBackoff)
	clear(w.pending)
	w.failLog.Warn(slog.Default(), "claude_agents", "claude agents --json failed; retrying in 5 min", "err", err)
	return true
}

func (w *agentsWatcher) reconcile(ctx context.Context, cfg Config, client *Client, active []activeMarker, rows map[string]agentRow, now time.Time) {
	next := map[string]pendingCorrection{}
	for _, a := range active {
		row, listed := rows[a.sessionID]
		if !listed {
			// Headless, SDK and nested sessions are never listed, so absence
			// alone proves nothing: check the owner process.
			if pid, start, ok := markerOwner(a.markerP); ok && !ownerAlive(pid, start) {
				reapMarker(ctx, client, a.markerP, a.lockP)
			}
			continue
		}
		want, ok := wantFor(a.m, row)
		if !ok {
			continue
		}
		prev, had := w.pending[a.sessionID]
		if had && prev.want == want && bytes.Equal(prev.body, a.body) && now.Sub(prev.seen) >= agentsConfirmAfter {
			applyCorrection(ctx, cfg, client, a, want)
			continue
		}
		if had && prev.want == want && bytes.Equal(prev.body, a.body) {
			next[a.sessionID] = prev
			continue
		}
		next[a.sessionID] = pendingCorrection{want: want, body: a.body, seen: now}
	}
	w.pending = next
}

// applyCorrection rewrites the marker only if it still holds the bytes both
// snapshots saw, then POSTs outside the lock like every other writer.
func applyCorrection(ctx context.Context, cfg Config, client *Client, a activeMarker, want correction) {
	var body []byte
	_ = withLockExWait(a.lockP, hookLockWait(cfg), func() error {
		cur, err := readMarker(a.markerP)
		if err != nil || !bytes.Equal(cur, a.body) {
			return nil
		}
		m := a.m
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
		slog.Info("claude agents corrected session", "session", a.sessionID, "from", a.m.State, "to", want.state)
		if err := postReconciled(ctx, cfg, client, a.markerP, a.lockP, body, hookLockWait(cfg)); err != nil {
			tickFailLog.Warn(slog.Default(), "claude_post", "status POST failed", "err", err)
		}
	}
}

// agentsWatchLoop runs beside the heartbeat in the daemon.
func agentsWatchLoop(ctx context.Context) {
	dir, err := stateDir()
	if err != nil {
		return
	}
	w := newAgentsWatcher(dir, claudeSessionsDir())
	ticker := time.NewTicker(agentsWatchEvery)
	defer ticker.Stop()
	var cfg Config
	var cfgOK, enabled bool
	var cfgAt time.Time
	for {
		if time.Since(cfgAt) >= agentsConfigEvery {
			cfgAt = time.Now()
			enabled = agentsPollEnabled()
			c, err := loadConfig()
			cfgOK = err == nil && c.Source != "" && c.ServerURL != ""
			cfg = c
		}
		if enabled && cfgOK {
			w.step(ctx, cfg, NewDaemonClient(cfg))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
