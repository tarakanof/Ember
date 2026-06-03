package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

// statuslineInput is the subset of Claude Code's statusline JSON we read.
type statuslineInput struct {
	SessionID  string `json:"session_id"`
	Cwd        string `json:"cwd"`
	RateLimits *struct {
		FiveHour *struct {
			UsedPercentage float64 `json:"used_percentage"`
			ResetsAt       int64   `json:"resets_at"`
		} `json:"five_hour"`
	} `json:"rate_limits"`
	ContextWindow *struct {
		UsedPercentage float64 `json:"used_percentage"`
	} `json:"context_window"`
}

func parseStatusline(b []byte) (statuslineInput, bool) {
	var in statuslineInput
	if json.Unmarshal(b, &in) != nil {
		return statuslineInput{}, false
	}
	return in, true
}

// extractRatePct returns the 5h rate-limit used-percentage as a clamped int
// (0..100), or (nil,false) when rate_limits.five_hour is absent.
func extractRatePct(in statuslineInput) (*int, bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil {
		return nil, false
	}
	pct := int(math.Round(in.RateLimits.FiveHour.UsedPercentage))
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct, true
}

// extractRateResetAt returns the 5h window's reset time (unix epoch seconds),
// or (0,false) when five_hour or a positive resets_at is absent.
func extractRateResetAt(in statuslineInput) (int64, bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil || in.RateLimits.FiveHour.ResetsAt <= 0 {
		return 0, false
	}
	return in.RateLimits.FiveHour.ResetsAt, true
}

// extractContextPct returns context_window.used_percentage as a clamped int
// (0..100), or (nil,false) when context_window is absent.
func extractContextPct(in statuslineInput) (*int, bool) {
	if in.ContextWindow == nil {
		return nil, false
	}
	pct := int(math.Round(in.ContextWindow.UsedPercentage))
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct, true
}

// contextPctEnabled reads EMBER_CONTEXT_PCT_ENABLED from producer.env (default
// true). Flag-only read — no token, no full loadConfig — so the statusline keeps
// its no-POST property while the menu glass toggle still gates the glass.
func contextPctEnabled() bool {
	path, err := envFilePath()
	if err != nil {
		return true
	}
	data, err := producer.ReadEnvFile(path)
	if err != nil {
		return true
	}
	switch strings.ToLower(data["EMBER_CONTEXT_PCT_ENABLED"]) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

func wrappedStatuslinePath(home string) string {
	return filepath.Join(home, ".config", "ember", "wrapped-statusline.json")
}

// readWrappedCommand returns the shell command captured from the user's
// original statusLine (string form, or {"command":...} object), or ("",false).
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

// runWrapped runs the captured statusline command via the shell with the
// original statusline JSON on its stdin, returning its stdout.
func runWrapped(command string, stdin []byte) ([]byte, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	return cmd.Output()
}

// ourStatuslineCommand is the statusLine command the installer sets. Stdout is
// NOT redirected (it's the rendered status bar Claude reads); only stderr goes
// to the producer log.
func ourStatuslineCommand(binPath string) string {
	return binPath + ` statusline 2>>$HOME/Library/Logs/ember-claude-producer.log`
}

// statusLineIsOurs reports whether a settings.json statusLine value is the
// command this producer installs (string or object form).
func statusLineIsOurs(v any) bool {
	raw, err := json.Marshal(v)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), "ember-claude-producer statusline")
}

// runStatusline is the `statusline` subcommand: read the statusline JSON from
// stdin, enrich the session marker with rate_window_pct (best-effort), then
// delegate to the user's captured statusline (stdout passed through). It does
// NOT call loadConfig and makes no network call — no token needed.
func runStatusline() {
	buf, _ := io.ReadAll(os.Stdin)
	if in, ok := parseStatusline(buf); ok {
		var ratePct, ctxPct *int
		var resetAt *int64
		if p, ok := extractRatePct(in); ok {
			ratePct = p
		}
		if r, ok := extractRateResetAt(in); ok {
			resetAt = &r
		}
		if contextPctEnabled() {
			if p, ok := extractContextPct(in); ok {
				ctxPct = p
			}
		}
		if ratePct != nil || ctxPct != nil || resetAt != nil {
			if dir, err := stateDir(); err == nil {
				_ = enrichMarker(dir, sanitizeSessionID(in.SessionID, in.Cwd), ratePct, ctxPct, resetAt)
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if cmd, ok := readWrappedCommand(wrappedStatuslinePath(home)); ok {
			if out, err := runWrapped(cmd, buf); err == nil {
				_, _ = os.Stdout.Write(out)
			}
		}
	}
	os.Exit(0)
}

// enrichMarker merges statusline-owned fields (rate_window_pct, context_pct)
// into an EXISTING session marker, preserving hook-set fields. Each pointer is
// applied only when non-nil; never clears. Absent/unparseable marker → skip.
func enrichMarker(stateDir, sessionID string, ratePct, ctxPct *int, resetAt *int64) error {
	mp := markerPath(stateDir, sessionID)
	lp := lockPath(stateDir, sessionID)
	return withLockEx(lp, func() error {
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
		out, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		return writeMarker(mp, out)
	})
}
