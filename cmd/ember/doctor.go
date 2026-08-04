package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

// CheckStatus is one of "ok" | "warn" | "fail" | "skipped".
type CheckStatus string

const (
	StatusOK      CheckStatus = "ok"
	StatusWarn    CheckStatus = "warn"
	StatusFail    CheckStatus = "fail"
	StatusSkipped CheckStatus = "skipped"
)

// CheckResult is one of the named checks in DoctorResult. The BaseURL/Source/
// Reachable/LastRediscoverAt/LastRediscoverResult fields are only populated by
// the `clock` check; every other check leaves them zero and they're omitted
// from JSON.
type CheckResult struct {
	Status               CheckStatus `json:"status"`
	Detail               string      `json:"detail,omitempty"`
	BaseURL              string      `json:"base_url,omitempty"`
	Source               string      `json:"source,omitempty"`
	Reachable            *bool       `json:"reachable,omitempty"`
	LastRediscoverAt     *int64      `json:"last_rediscover_at,omitempty"`
	LastRediscoverResult string      `json:"last_rediscover_result,omitempty"`
}

// DoctorResult is the full diagnostic. OK is true when no check has StatusFail
// or StatusSkipped. Warn does NOT flip OK — a stale-but-configured meetings feed
// should not make monitoring see the server as unavailable.
// Skipped IS non-OK (offline mode is partial by design); automation must treat
// OK==false as either failed or partial and inspect Mode + per-check Status.
type DoctorResult struct {
	OK     bool                   `json:"ok"`
	Mode   string                 `json:"mode"` // "online" or "offline"
	Checks map[string]CheckResult `json:"checks"`
}

// runDoctorChecks runs all named checks. With app==nil, runtime checks are
// marked skipped (offline pre-flight mode). Otherwise online: the running
// server's state is used.
func runDoctorChecks(ctx context.Context, app *App, cfg *Config) DoctorResult {
	res := DoctorResult{Checks: make(map[string]CheckResult, 10)}
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

	// 9. meetings  (skipped offline)
	if app == nil {
		res.Checks["meetings"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["meetings"] = checkMeetings(app, cfg)
	}

	// 10. clock  (skipped offline; needs app state for source + rediscover history)
	if app == nil {
		res.Checks["clock"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["clock"] = checkClock(ctx, app)
	}

	// 11. capabilities  (skipped offline; the cache lives in the running server)
	if app == nil {
		res.Checks["capabilities"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["capabilities"] = checkCapabilities(app)
	}

	// StatusWarn is non-fatal: a stale meetings feed (or the startup window
	// before the first ICS poll) must not make /admin/doctor return 503 and
	// must not make `ember doctor` exit 1 on the online path.
	// StatusSkipped IS fatal (offline mode is partial by design — existing
	// semantics preserved: TestRunDoctorChecks_OfflineMarksRuntimeSkipped
	// asserts OK==false when skipped checks are present).
	res.OK = true
	for _, c := range res.Checks {
		if c.Status == StatusFail || c.Status == StatusSkipped {
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
	url := cfg.AWTRIX.HTTPBaseURL + "/api/v1/device"
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

// checkCapabilities reports the cached firmware capability counts. A missing
// cache is a Warn, not a Fail: the clock was simply unreachable at startup, and
// GET /v1/device/capabilities still answers from a live fetch.
func checkCapabilities(app *App) CheckResult {
	caps, ok := app.capabilities()
	if !ok {
		return CheckResult{Status: StatusWarn, Detail: "not fetched (clock unreachable at startup?)"}
	}
	fw := app.deviceFirmware()
	if fw == "" {
		fw = "<unknown>"
	}
	return CheckResult{Status: StatusOK, Detail: fmt.Sprintf(
		"effects=%d palette_effects=%d transitions=%d overlays=%d palettes=%d radio=%t firmware=%s",
		len(caps.Effects), len(caps.PaletteEffects), len(caps.Transitions),
		len(caps.Overlays), len(caps.Palettes), caps.Radio, fw)}
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

// checkMeetings reports the state of the meetings / ICS calendar feature.
// Covers: not configured, URLs present but never fetched or stale, and healthy
// with an upcoming count. URL strings never appear in the output — they are
// credentials. Feed count and next-meeting title are safe.
func checkMeetings(app *App, cfg *Config) CheckResult {
	if len(app.meetingsURLs) == 0 {
		return CheckResult{Status: StatusOK, Detail: "not configured (EMBER_MEETINGS_ICS_URLS unset)"}
	}
	if !cfg.Meetings.IsEnabled() {
		return CheckResult{Status: StatusOK, Detail: fmt.Sprintf("%d feed(s) configured but meetings disabled", len(app.meetingsURLs))}
	}
	feedCount := len(app.meetingsURLs)
	now := time.Now()
	lastOK := app.meetings.lastOK()
	if lastOK.IsZero() {
		return CheckResult{
			Status: StatusWarn,
			Detail: fmt.Sprintf("%d feed(s); never successfully fetched", feedCount),
		}
	}
	age := now.Sub(lastOK)
	if age >= meetingsStaleTTL {
		return CheckResult{
			Status: StatusWarn,
			Detail: fmt.Sprintf("%d feed(s); last successful fetch %v ago (stale)", feedCount, age.Round(time.Second)),
		}
	}
	occ, ok := app.meetings.next(now)
	if !ok {
		return CheckResult{
			Status: StatusOK,
			Detail: fmt.Sprintf("%d feed(s); no upcoming meetings", feedCount),
		}
	}
	mins := meetingMinutes(now, occ.Start)
	return CheckResult{
		Status: StatusOK,
		Detail: fmt.Sprintf("%d feed(s); next: %s in %dm", feedCount, sanitizeMeetingTitle(occ.Title), mins),
	}
}

// checkClock reports live reachability of the effective clock URL alongside
// where that URL came from (deviceSource) and the outcome of the most recent
// self-healing re-discovery attempt (T1/T2). Unlike awtrix_reachable (which
// is fail-capable and part of the older static check), a transient blip here
// only warns — the periodic probe (StartDeviceWatch) is expected to recover
// it, so doctor must not 503 on a momentary miss.
func checkClock(ctx context.Context, app *App) CheckResult {
	baseURL := app.cfg.Load().AWTRIX.HTTPBaseURL
	source := app.deviceSource()

	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	cl := &http.Client{Timeout: 1500 * time.Millisecond}
	_, reachable := discovery.Reachable(probeCtx, cl, baseURL)

	var lastAt *int64
	if v := app.lastRediscoverAt.Load(); v != 0 {
		lastAt = &v
	}
	lastResult, _ := app.lastRediscoverResult.Load().(string)

	status := StatusOK
	detail := fmt.Sprintf("base_url=%s source=%s reachable=%v", baseURL, source, reachable)
	if !reachable {
		status = StatusWarn
		detail += " (unreachable; periodic re-discovery probe will retry)"
	}

	return CheckResult{
		Status:               status,
		Detail:               detail,
		BaseURL:              baseURL,
		Source:               source,
		Reachable:            &reachable,
		LastRediscoverAt:     lastAt,
		LastRediscoverResult: lastResult,
	}
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

	// doctor is a standalone CLI invocation with no app-wide structured
	// logger; build a bare stderr one so baseline-repair warnings from
	// loadConfig are still visible instead of silently dropped.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := loadConfig(*configPath, logger)
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
		case StatusWarn:
			marker = "[WARN]   "
		case StatusFail:
			marker = "[FAIL]   "
		case StatusSkipped:
			marker = "[SKIP]   "
		}
		fmt.Fprintf(w, "%s %-*s  %s\n", marker, maxKey, k, c.Detail)
	}
	failCount, skipCount, warnCount := 0, 0, 0
	for _, c := range res.Checks {
		switch c.Status {
		case StatusFail:
			failCount++
		case StatusSkipped:
			skipCount++
		case StatusWarn:
			warnCount++
		}
	}
	switch {
	case failCount > 0:
		fmt.Fprintf(w, "\nFAIL (%d failed, %d skipped, mode=%s)\n", failCount, skipCount, res.Mode)
	case res.Mode == "offline":
		fmt.Fprintf(w, "\nOK (offline, partial — %d skipped)\n", skipCount)
	case warnCount > 0:
		fmt.Fprintf(w, "\nOK (%d warning(s), online)\n", warnCount)
	default:
		fmt.Fprintln(w, "\nOK (online)")
	}
}
