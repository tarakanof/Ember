package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
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
			res.Checks["auth_token_present"] = CheckResult{Status: StatusFail, Detail: "EMBER_TOKEN env unset"}
		} else {
			res.Checks["auth_token_present"] = CheckResult{Status: StatusOK, Detail: fmt.Sprintf("env=%s length=%d", app.cfg.Load().Auth.StatusTokenEnv, len(tok))}
		}
	}

	// 3. awtrix_reachable (detail notes where the clock URL came from:
	// store override / config.json / mDNS discovery)
	awtrixCheck := checkAWTRIXReachable(ctx, cfg)
	if app != nil {
		awtrixCheck.Detail += fmt.Sprintf(" [source=%s]", app.deviceSource())
	}
	res.Checks["awtrix_reachable"] = awtrixCheck

	// 4. http_listening (skipped offline)
	if app == nil || app.listener == nil {
		res.Checks["http_listening"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		scheme := "http"
		if os.Getenv(envTLSCertFile) != "" {
			scheme = "https"
		}
		res.Checks["http_listening"] = CheckResult{
			Status: StatusOK,
			Detail: fmt.Sprintf("addr=%s scheme=%s", app.listener.Addr().String(), scheme),
		}
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

// runDoctor parses doctor-specific flags from args, runs the diagnostic
// online (against --server-url) or offline (--offline / fallback after
// network error), and prints the result. Exits 0 if healthy, 1 otherwise.
func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config JSON file")
	serverURL := fs.String("server-url", "http://127.0.0.1:3627", "doctor server URL (online mode)")
	offline := fs.Bool("offline", false, "skip the server probe; run static checks only")
	asJSON := fs.Bool("json", false, "print result as JSON")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "doctor: config:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res DoctorResult
	if *offline {
		res = runDoctorChecks(ctx, nil, &cfg)
	} else {
		online, terr := tryAdminDoctor(ctx, *serverURL, os.Getenv(cfg.Auth.StatusTokenEnv))
		switch {
		case terr == errAuthFailure:
			fmt.Fprintf(os.Stderr, "auth failure: %s/admin/doctor returned 401 — check EMBER_TOKEN\n", *serverURL)
			os.Exit(1)
		case terr != nil:
			res = runDoctorChecks(ctx, nil, &cfg)
		default:
			res = online
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	} else {
		renderDoctorText(os.Stdout, res)
	}

	exit := 0
	switch res.Mode {
	case "online":
		if !res.OK {
			exit = 1
		}
	case "offline":
		// In offline mode, failures of static checks are real failures.
		// Skipped checks are expected and don't count.
		for _, c := range res.Checks {
			if c.Status == StatusFail {
				exit = 1
				break
			}
		}
	}
	os.Exit(exit)
}

var errAuthFailure = errors.New("auth failure (401)")

// tryAdminDoctor performs GET /admin/doctor. Returns errAuthFailure on 401.
// Returns a transport error on dial/connect issues. Otherwise returns the
// decoded result.
func tryAdminDoctor(ctx context.Context, base, token string) (DoctorResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/admin/doctor", nil)
	if err != nil {
		return DoctorResult{}, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return DoctorResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		return DoctorResult{}, errAuthFailure
	}
	var res DoctorResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return DoctorResult{}, err
	}
	return res, nil
}

// renderDoctorText prints a human-readable check table to w.
func renderDoctorText(w io.Writer, res DoctorResult) {
	keys := make([]string, 0, len(res.Checks))
	for k := range res.Checks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	maxKey := 0
	for _, k := range keys {
		if len(k) > maxKey {
			maxKey = len(k)
		}
	}
	for _, k := range keys {
		c := res.Checks[k]
		marker := "[OK]     "
		switch c.Status {
		case StatusFail:
			marker = "[FAIL]   "
		case StatusSkipped:
			marker = "[SKIP]   "
		}
		fmt.Fprintf(w, "%s %-*s  %s\n", marker, maxKey, k, c.Detail)
	}
	failCount, skipCount := 0, 0
	for _, c := range res.Checks {
		switch c.Status {
		case StatusFail:
			failCount++
		case StatusSkipped:
			skipCount++
		}
	}
	switch {
	case failCount > 0:
		fmt.Fprintf(w, "\nFAIL (%d failed, %d skipped, mode=%s)\n", failCount, skipCount, res.Mode)
	case res.Mode == "offline":
		fmt.Fprintf(w, "\nOK (offline, partial — %d skipped)\n", skipCount)
	default:
		fmt.Fprintln(w, "\nOK (online)")
	}
}
