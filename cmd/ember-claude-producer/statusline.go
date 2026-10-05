package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type statuslineInput struct {
	SessionID  string `json:"session_id"`
	Cwd        string `json:"cwd"`
	RateLimits *struct {
		FiveHour *struct {
			UsedPercentage float64 `json:"used_percentage"`
			ResetsAt       int64   `json:"resets_at"`
		} `json:"five_hour"`
		// SevenDay is the weekly rate-limit window, shaped like five_hour and parsed leniently.
		SevenDay *struct {
			UsedPercentage float64 `json:"used_percentage"`
			ResetsAt       int64   `json:"resets_at"`
		} `json:"seven_day"`
	} `json:"rate_limits"`
	ContextWindow *struct {
		// UsedPercentage is null early in a session: unknown, not 0 %.
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
}

func parseStatusline(b []byte) (statuslineInput, bool) {
	var in statuslineInput
	if json.Unmarshal(b, &in) != nil {
		return statuslineInput{}, false
	}
	return in, true
}

func extractRatePct(in statuslineInput) (*int, bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil {
		return nil, false
	}
	pct := producer.Pct(in.RateLimits.FiveHour.UsedPercentage)
	return &pct, true
}

func extractRateResetAt(in statuslineInput) (int64, bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil || in.RateLimits.FiveHour.ResetsAt <= 0 {
		return 0, false
	}
	return in.RateLimits.FiveHour.ResetsAt, true
}

func extractRateResetLabel(in statuslineInput) (string, bool) {
	at, ok := extractRateResetAt(in)
	if !ok {
		return "", false
	}
	return time.Unix(at, 0).Local().Format("15:04"), true
}

func extractWeekPct(in statuslineInput) (*int, bool) {
	if in.RateLimits == nil || in.RateLimits.SevenDay == nil {
		return nil, false
	}
	pct := producer.Pct(in.RateLimits.SevenDay.UsedPercentage)
	return &pct, true
}

func extractWeekResetAt(in statuslineInput) (int64, bool) {
	if in.RateLimits == nil || in.RateLimits.SevenDay == nil || in.RateLimits.SevenDay.ResetsAt <= 0 {
		return 0, false
	}
	return in.RateLimits.SevenDay.ResetsAt, true
}

func extractWeekResetLabel(in statuslineInput) (string, bool) {
	at, ok := extractWeekResetAt(in)
	if !ok {
		return "", false
	}
	return dayLabel(time.Unix(at, 0), time.Local), true
}

func extractContextPct(in statuslineInput) (*int, bool) {
	if in.ContextWindow == nil || in.ContextWindow.UsedPercentage == nil {
		return nil, false
	}
	pct := producer.Pct(*in.ContextWindow.UsedPercentage)
	return &pct, true
}

// statuslineEnv reads producer.env for the statusline, which never calls
// loadConfig; nil when it is unreadable.
func statuslineEnv() map[string]string {
	path, err := envFilePath()
	if err != nil {
		return nil
	}
	data, err := producer.ReadEnvFile(path)
	if err != nil {
		return nil
	}
	return data
}

func contextPctEnabled(env map[string]string) bool {
	return producer.Bool(env["EMBER_CONTEXT_PCT_ENABLED"], true)
}

// defaultWrappedTimeout bounds the user's own status line command, so a
// hung one cannot hang ours (Claude Code waits on our stdout).
const defaultWrappedTimeout = 10 * time.Second

// wrappedTimeout is EMBER_STATUSLINE_TIMEOUT_MS from the environment, else
// producer.env, else defaultWrappedTimeout.
func wrappedTimeout(env map[string]string) time.Duration {
	v, ok := os.LookupEnv("EMBER_STATUSLINE_TIMEOUT_MS")
	if !ok {
		v = env["EMBER_STATUSLINE_TIMEOUT_MS"]
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return time.Duration(n) * time.Millisecond
	}
	return defaultWrappedTimeout
}

func wrappedStatuslinePath(home string) string {
	return filepath.Join(home, ".config", "ember", "wrapped-statusline.json")
}

func readWrappedCommand(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, t != ""
	case map[string]any:
		if c, ok := t["command"].(string); ok && c != "" {
			return c, true
		}
	}
	return "", false
}

// errWrappedTimeout means the wrapped command ran past its timeout.
var errWrappedTimeout = errors.New("wrapped status line timed out")

// runWrapped runs the wrapped status line command with stdin. On timeout its
// whole process group is killed, so a background child still holding stdout
// cannot keep the read open. A command that exited 0 but left such a child
// returns its complete output with exec.ErrWaitDelay.
func runWrapped(command string, stdin []byte, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return out, errWrappedTimeout
	}
	return out, err
}

// wrappedOutput is what the wrapped command printed. A good run is cached
// per session (rewritten only when it changed); a timed-out run shows the
// cached output rather than a blank status line. A failing command prints
// nothing, as before.
func wrappedOutput(command string, stdin []byte, timeout time.Duration, cachePath string) []byte {
	out, err := runWrapped(command, stdin, timeout)
	switch {
	case err == nil || errors.Is(err, exec.ErrWaitDelay):
		if cachePath != "" {
			if prev, rerr := os.ReadFile(cachePath); rerr != nil || !bytes.Equal(prev, out) {
				_ = os.MkdirAll(filepath.Dir(cachePath), 0o700)
				_ = os.WriteFile(cachePath, out, 0o600)
				pruneStatuslineCache(filepath.Dir(cachePath))
			}
		}
		return out
	case errors.Is(err, errWrappedTimeout) && cachePath != "":
		cached, _ := os.ReadFile(cachePath)
		return cached
	}
	return nil
}

// statuslineCacheTTL is how long a session's cached wrapped output is kept.
const statuslineCacheTTL = 24 * time.Hour

func statuslineCachePath(sessionID string) string {
	dir, err := stateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), "statusline", sessionID+".out")
}

func pruneStatuslineCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-statuslineCacheTTL)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func ourStatuslineCommand(binPath string) string {
	return binPath + ` statusline 2>>` + producer.LogDirShell() + `/ember-claude-producer.log`
}

func statusLineIsOurs(v any) bool {
	raw, err := json.Marshal(v)
	if err != nil {
		return false
	}
	s := string(raw)
	return strings.Contains(s, producerName+" statusline") ||
		strings.Contains(s, legacyProducerName+" statusline")
}

func runStatusline() {
	buf, _ := io.ReadAll(os.Stdin)
	env := statuslineEnv()
	in, parsed := parseStatusline(buf)
	if parsed {
		var ratePct, ctxPct, weekPct *int
		var resetAt, weekResetAt *int64
		var resetLabel, weekResetLabel string
		if p, ok := extractRatePct(in); ok {
			ratePct = p
		}
		if r, ok := extractRateResetAt(in); ok {
			resetAt = &r
		}
		if l, ok := extractRateResetLabel(in); ok {
			resetLabel = l
		}
		if p, ok := extractWeekPct(in); ok {
			weekPct = p
		}
		if r, ok := extractWeekResetAt(in); ok {
			weekResetAt = &r
		}
		if l, ok := extractWeekResetLabel(in); ok {
			weekResetLabel = l
		}
		if contextPctEnabled(env) {
			if p, ok := extractContextPct(in); ok {
				ctxPct = p
			}
		}
		if ratePct != nil || ctxPct != nil || resetAt != nil || resetLabel != "" ||
			weekPct != nil || weekResetAt != nil || weekResetLabel != "" {
			if dir, err := stateDir(); err == nil {
				_ = enrichMarker(dir, sanitizeSessionID(in.SessionID, in.Cwd),
					ratePct, ctxPct, resetAt, resetLabel, weekPct, weekResetAt, weekResetLabel)
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if cmd, ok := readWrappedCommand(wrappedStatuslinePath(home)); ok {
			cache := ""
			if parsed {
				cache = statuslineCachePath(sanitizeSessionID(in.SessionID, in.Cwd))
			}
			_, _ = os.Stdout.Write(wrappedOutput(cmd, buf, wrappedTimeout(env), cache))
		}
	}
	os.Exit(0)
}

const statuslineLockWait = 250 * time.Millisecond

// statuslineRefresh is how long (by file mtime) an unchanged marker goes
// without a statusline rewrite: Claude Code refreshes the status line many times a
// second while streaming, and each write was an fsync.
const statuslineRefresh = time.Minute

func enrichMarker(stateDir, sessionID string, ratePct, ctxPct *int, resetAt *int64, resetLabel string,
	weekPct *int, weekResetAt *int64, weekResetLabel string) error {
	mp := markerPath(stateDir, sessionID)
	lp := lockPath(stateDir, sessionID)
	// The status line renders synchronously: drop this sample rather than
	// wait behind a hook's in-flight POST; the next refresh carries it.
	return withLockExWait(lp, statuslineLockWait, func() error {
		body, err := readMarker(mp)
		if err != nil {
			return nil
		}
		var m marker
		if json.Unmarshal(body, &m) != nil {
			return nil
		}
		if ratePct != nil {
			m.RateWindowPct = ratePct
		}
		if ctxPct != nil {
			m.ContextPct = ctxPct
		}
		if resetAt != nil {
			m.RateResetAt = *resetAt
		}
		if resetLabel != "" {
			m.RateResetLabel = resetLabel
		}
		if weekPct != nil {
			m.RateWeekPct = weekPct
		}
		if weekResetAt != nil {
			m.RateWeekResetAt = *weekResetAt
		}
		if weekResetLabel != "" {
			m.RateWeekResetLabel = weekResetLabel
		}
		now := hookNow()
		if same, err := json.Marshal(m); err == nil && bytes.Equal(same, body) {
			// Unchanged: rewrite only to keep the mtime fresh for the TTL.
			if fi, err := os.Stat(mp); err == nil && now.Sub(fi.ModTime()) < statuslineRefresh {
				return nil
			}
		} else {
			m.StatuslineChangedMs = now.UnixMilli()
		}
		m.StatuslineAt = now.Unix()
		out, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		return writeMarker(mp, out)
	})
}
