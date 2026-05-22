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
)

// statuslineInput is the subset of Claude Code's statusline JSON we read.
type statuslineInput struct {
	SessionID  string `json:"session_id"`
	Cwd        string `json:"cwd"`
	RateLimits *struct {
		FiveHour *struct {
			UsedPercentage float64 `json:"used_percentage"`
		} `json:"five_hour"`
	} `json:"rate_limits"`
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

func wrappedStatuslinePath(home string) string {
	return filepath.Join(home, ".config", "awtrix-ai-status", "wrapped-statusline.json")
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
	return binPath + ` statusline 2>>$HOME/Library/Logs/awtrix-claude-producer.log`
}

// statusLineIsOurs reports whether a settings.json statusLine value is the
// command this producer installs (string or object form).
func statusLineIsOurs(v any) bool {
	raw, err := json.Marshal(v)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), "awtrix-claude-producer statusline")
}

// runStatusline is the `statusline` subcommand: read the statusline JSON from
// stdin, enrich the session marker with rate_window_pct (best-effort), then
// delegate to the user's captured statusline (stdout passed through). It does
// NOT call loadConfig and makes no network call — no token needed.
func runStatusline() {
	buf, _ := io.ReadAll(os.Stdin)
	if in, ok := parseStatusline(buf); ok {
		if pct, ok := extractRatePct(in); ok {
			if dir, err := stateDir(); err == nil {
				_ = enrichMarkerRate(dir, sanitizeSessionID(in.SessionID, in.Cwd), *pct)
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

// enrichMarkerRate merges rate_window_pct into an EXISTING session marker,
// preserving all hook-set fields. All checks happen inside the same flock the
// hooks take. Enrich-only: absent marker (hooks own creation) or unparseable
// marker (don't clobber hook data) → left untouched.
func enrichMarkerRate(stateDir, sessionID string, pct int) error {
	mp := markerPath(stateDir, sessionID)
	lp := lockPath(stateDir, sessionID)
	return withLockEx(lp, func() error {
		body, err := readMarker(mp)
		if err != nil {
			return nil // absent/unreadable → skip
		}
		var req StatusRequest
		if json.Unmarshal(body, &req) != nil {
			return nil // unparseable → skip (data-loss guard)
		}
		p := pct
		req.RateWindowPct = &p
		out, err := json.Marshal(req)
		if err != nil {
			return nil
		}
		return writeMarker(mp, out)
	})
}
