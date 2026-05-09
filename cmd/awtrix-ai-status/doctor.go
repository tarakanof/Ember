package main

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"time"
)

// CheckStatus is one of "ok" | "fail" | "skipped".
type CheckStatus string

const (
	StatusOK      CheckStatus = "ok"
	StatusFail    CheckStatus = "fail"
	StatusSkipped CheckStatus = "skipped"
)

// CheckResult is one of the eight named checks in DoctorResult.
type CheckResult struct {
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// DoctorResult is the full diagnostic. OK is the AND of every Checks[k].Status == StatusOK.
// Skipped is NOT ok — automation should treat OK==true as healthy and OK==false as
// either failed or partial (look at Mode + per-check Status).
type DoctorResult struct {
	OK     bool                   `json:"ok"`
	Mode   string                 `json:"mode"` // "online" or "offline"
	Checks map[string]CheckResult `json:"checks"`
}

// runDoctorChecks runs the eight checks. With app==nil, runtime checks are
// marked skipped (offline pre-flight mode). Otherwise online: the running
// server's state is used.
func runDoctorChecks(ctx context.Context, app *App, cfg *Config) DoctorResult {
	res := DoctorResult{Checks: make(map[string]CheckResult, 8)}
	if app == nil {
		res.Mode = "offline"
	} else {
		res.Mode = "online"
	}

	// 1. config_loaded
	if cfg == nil {
		res.Checks["config_loaded"] = CheckResult{Status: StatusFail, Detail: "no config loaded"}
	} else {
		path, src := "<unknown>", "<unknown>"
		if app != nil {
			path, src = app.configPath, app.configSource
		}
		res.Checks["config_loaded"] = CheckResult{
			Status: StatusOK,
			Detail: fmt.Sprintf("path=%s source=%s", path, src),
		}
	}

	// 2. auth_token_present  (skipped offline)
	if app == nil {
		res.Checks["auth_token_present"] = CheckResult{Status: StatusSkipped, Detail: "server not running; operator env != container env"}
	} else {
		tok := app.cfg.Load().Auth.StatusToken
		if tok == "" {
			res.Checks["auth_token_present"] = CheckResult{Status: StatusFail, Detail: "STATUS_TOKEN env unset"}
		} else {
			res.Checks["auth_token_present"] = CheckResult{Status: StatusOK, Detail: fmt.Sprintf("env=%s length=%d", app.cfg.Load().Auth.StatusTokenEnv, len(tok))}
		}
	}

	// 3. awtrix_reachable
	res.Checks["awtrix_reachable"] = checkAWTRIXReachable(ctx, cfg)

	// 4. http_listening (skipped offline)
	if app == nil || app.listener == nil {
		res.Checks["http_listening"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["http_listening"] = CheckResult{Status: StatusOK, Detail: "addr=" + app.listener.Addr().String()}
	}

	// 5. sessions_summary  (skipped offline)
	if app == nil {
		res.Checks["sessions_summary"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["sessions_summary"] = checkSessionsSummary(app)
	}

	// 6. last_publish  (skipped offline)
	if app == nil {
		res.Checks["last_publish"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["last_publish"] = checkLastPublish(app)
	}

	// 7. uptime  (skipped offline)
	if app == nil {
		res.Checks["uptime"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["uptime"] = CheckResult{Status: StatusOK, Detail: time.Since(app.startedAt).Round(time.Second).String()}
	}

	// 8. build
	res.Checks["build"] = checkBuild()

	res.OK = true
	for _, c := range res.Checks {
		if c.Status != StatusOK {
			res.OK = false
			break
		}
	}
	return res
}

func checkAWTRIXReachable(ctx context.Context, cfg *Config) CheckResult {
	if cfg == nil || cfg.AWTRIX.HTTPBaseURL == "" {
		return CheckResult{Status: StatusFail, Detail: "awtrix.http_base_url empty"}
	}
	timeout := time.Duration(cfg.AWTRIX.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	url := cfg.AWTRIX.HTTPBaseURL + "/api/stats"
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CheckResult{Status: StatusFail, Detail: fmt.Sprintf("GET %s: %v", url, err)}
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Status: StatusFail, Detail: fmt.Sprintf("GET %s: %v", url, err)}
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Round(time.Millisecond)
	if resp.StatusCode >= 500 {
		return CheckResult{Status: StatusFail, Detail: fmt.Sprintf("GET %s → %d (%v)", url, resp.StatusCode, elapsed)}
	}
	return CheckResult{Status: StatusOK, Detail: fmt.Sprintf("GET %s → %d (%v)", url, resp.StatusCode, elapsed)}
}

func checkSessionsSummary(app *App) CheckResult {
	app.mu.Lock()
	defer app.mu.Unlock()
	total := len(app.sessions)
	byState := map[string]int{}
	var oldest time.Time
	now := time.Now()
	for _, s := range app.sessions {
		byState[string(s.State)]++
		if oldest.IsZero() || s.UpdatedAt.Before(oldest) {
			oldest = s.UpdatedAt
		}
	}
	detail := fmt.Sprintf("total=%d", total)
	for state, n := range byState {
		detail += fmt.Sprintf(" %s=%d", state, n)
	}
	if !oldest.IsZero() {
		detail += fmt.Sprintf(" oldest_age=%v", now.Sub(oldest).Round(time.Second))
	}
	return CheckResult{Status: StatusOK, Detail: detail}
}

func checkLastPublish(app *App) CheckResult {
	app.mu.Lock()
	at, ok, lastErr := app.lastPublishAt, app.lastPublishOK, app.lastPublishErr
	app.mu.Unlock()
	if at.IsZero() {
		return CheckResult{Status: StatusOK, Detail: "no publish attempted yet"}
	}
	if !ok {
		return CheckResult{Status: StatusFail, Detail: fmt.Sprintf("at=%s ok=false err=%s", at.Format(time.RFC3339), lastErr)}
	}
	return CheckResult{Status: StatusOK, Detail: fmt.Sprintf("at=%s ok=true err=<none>", at.Format(time.RFC3339))}
}

func checkBuild() CheckResult {
	rev, modified := "unknown", false
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	dirty := ""
	if modified {
		dirty = "+dirty"
	}
	return CheckResult{Status: StatusOK, Detail: fmt.Sprintf("rev=%s%s go=%s", rev, dirty, runtime.Version())}
}
