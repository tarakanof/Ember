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

	"github.com/tarakanof/ember/internal/awtrix"
)

// CheckStatus is one of "ok" | "warn" | "fail" | "skipped".
type CheckStatus string

const (
	StatusOK      CheckStatus = "ok"
	StatusWarn    CheckStatus = "warn"
	StatusFail    CheckStatus = "fail"
	StatusSkipped CheckStatus = "skipped"
)

// CheckResult is one of the named checks in DoctorResult.
type CheckResult struct {
	Status               CheckStatus `json:"status"`
	Detail               string      `json:"detail,omitempty"`
	BaseURL              string      `json:"base_url,omitempty"`
	Source               string      `json:"source,omitempty"`
	Reachable            *bool       `json:"reachable,omitempty"`
	LastRediscoverAt     *int64      `json:"last_rediscover_at,omitempty"`
	LastRediscoverResult string      `json:"last_rediscover_result,omitempty"`
}

// DoctorResult is the full diagnostic.
type DoctorResult struct {
	OK bool `json:"ok"`
	// Mode is "online" or "offline".
	Mode   string                 `json:"mode"`
	Checks map[string]CheckResult `json:"checks"`
}

func runDoctorChecks(ctx context.Context, app *App, cfg *Config) DoctorResult {
	res := DoctorResult{Checks: make(map[string]CheckResult, 10)}
	if app == nil {
		res.Mode = "offline"
	} else {
		res.Mode = "online"
	}

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

	awtrixCheck := checkAWTRIXReachable(ctx, cfg)
	if app != nil {
		awtrixCheck.Detail += fmt.Sprintf(" [source=%s]", app.deviceSource())
	}
	res.Checks["awtrix_reachable"] = awtrixCheck

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

	if app == nil {
		res.Checks["sessions_summary"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["sessions_summary"] = checkSessionsSummary(app)
	}

	if app == nil {
		res.Checks["devices"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["devices"] = checkDevices(app)
	}

	if app == nil {
		res.Checks["last_publish"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["last_publish"] = checkLastPublish(app)
	}

	if app == nil {
		res.Checks["uptime"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["uptime"] = CheckResult{Status: StatusOK, Detail: time.Since(app.startedAt).Round(time.Second).String()}
	}

	res.Checks["build"] = checkBuild()

	if app == nil {
		res.Checks["meetings"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["meetings"] = checkMeetings(app, cfg)
	}

	if app == nil {
		res.Checks["clock"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["clock"] = checkClock(ctx, app)
	}

	if app == nil {
		res.Checks["capabilities"] = CheckResult{Status: StatusSkipped, Detail: "server not running"}
	} else {
		res.Checks["capabilities"] = checkCapabilities(app)
	}

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
	if cfg == nil || cfg.effectiveClockURL() == "" {
		return CheckResult{Status: StatusFail, Detail: "awtrix.http_base_url empty"}
	}
	cl, err := newClockAccess(func() *Config { return cfg }).client(callDoctor)
	if err != nil {
		return CheckResult{Status: StatusFail, Detail: "awtrix.http_base_url: " + err.Error()}
	}
	url := cl.BaseURL() + awtrix.DevicePath
	start := time.Now()
	reply, err := cl.RawDevice(ctx)
	if err != nil {
		return CheckResult{Status: StatusFail, Detail: fmt.Sprintf("GET %s: %v", url, err)}
	}
	elapsed := time.Since(start).Round(time.Millisecond)
	if reply.Status >= 500 {
		return CheckResult{Status: StatusFail, Detail: fmt.Sprintf("GET %s → %d (%v)", url, reply.Status, elapsed)}
	}
	return CheckResult{Status: StatusOK, Detail: fmt.Sprintf("GET %s → %d (%v)", url, reply.Status, elapsed)}
}

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
		"effects=%d palette_effects=%d transitions=%d overlays=%d palettes=%d buzzer=%t firmware=%s",
		len(caps.Effects), len(caps.PaletteEffects), len(caps.Transitions),
		len(caps.Overlays), len(caps.Palettes), caps.Audio.Buzzer, fw)}
}

const deviceStaleAfter = 5 * time.Minute

func checkDevices(app *App) CheckResult {
	if err := app.devices.loadError(); err != nil {
		return CheckResult{Status: StatusFail, Detail: "registry load failed (writes refused until restart): " + err.Error()}
	}
	devices := app.devices.list()
	now := app.devices.now()
	status := StatusOK
	detail := fmt.Sprintf("registered=%d", len(devices))
	for _, d := range devices {
		if d.LastCheckin == nil {
			status = StatusWarn
			detail += fmt.Sprintf(" %s never checked in", d.ID)
			continue
		}
		age := now.Sub(d.LastCheckin.SeenAt).Truncate(time.Second)
		if age > deviceStaleAfter {
			status = StatusWarn
		}
		detail += fmt.Sprintf(" %s seen=%v ago", d.ID, age)
	}
	return CheckResult{Status: status, Detail: detail}
}

func checkSessionsSummary(app *App) CheckResult {
	v := app.sessions.View()
	total := len(v.Sessions)
	byState := map[string]int{}
	var oldest time.Time
	now := v.Now
	for _, s := range v.Sessions {
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

func checkClock(ctx context.Context, app *App) CheckResult {
	baseURL, source := app.cfg.Load().clockURL()

	probeCtx, cancel := context.WithTimeout(ctx, probeCallTimeout)
	defer cancel()
	reachable := app.clock.reachable(probeCtx, baseURL)

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

func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config JSON file")
	serverURL := fs.String("server-url", "http://127.0.0.1:3627", "doctor server URL (online mode)")
	offline := fs.Bool("offline", false, "skip the server probe; run static checks only")
	asJSON := fs.Bool("json", false, "print result as JSON")
	_ = fs.Parse(args)

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
