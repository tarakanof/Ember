package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/discovery"
)

// clockAccess is the server's one way to reach the awtrix-ng clock. It
// resolves the clock from the live config on every call (so rediscovery and
// PUT /v1/device/config apply at once), applies one URL rule and one timeout
// per call class, serialises read-merge-writes of the clock's system object,
// and is the only code in cmd/ember that constructs an awtrix client. The
// error map (writeClockError) and the retry rule (retryClockCall) sit here
// too, so every question of "how do we talk to the clock" has one answer.
//
// Server-initiated writes reach it through the Publisher seam
// (clockPublisher in publisher.go, wrapped by quietPublisher); the menu's
// /v1/device/* handlers, the watch loop and doctor call its methods directly.
// It deliberately has no Notify/PlayRTTTL of its own, so a.clock can't be
// used to sound the clock past the quiet-hours gate.
type clockAccess struct {
	cfg func() *Config

	// systemLock (capacity 1) serialises read-merge-PUTs of /api/v1/system.
	// The object holds the Wi-Fi credentials, the sensor offsets and the
	// button callback, and NG only offers a full replace, so two
	// unserialised writers lose one write. A channel rather than a mutex so
	// a waiter whose request is cancelled stops queueing behind a stuck
	// clock (a holder can take two menu-class calls, up to 16s).
	systemLock chan struct{}
}

func newClockAccess(cfg func() *Config) *clockAccess {
	return &clockAccess{cfg: cfg, systemLock: make(chan struct{}, 1)}
}

// callClass picks a call's timeout. Each is the whole budget for one HTTP
// exchange; a caller's context can only shorten it.
type callClass int

const (
	// callPublish is a server-initiated write through Publisher. The ceiling
	// is awtrix.timeout_seconds; the coordinator's retry narrows each attempt
	// to publishAttemptTimeout through its context.
	callPublish callClass = iota
	// callMenu is a menu-initiated /v1/device/* call, the boot-ping script
	// sync and the dashboard's health probe.
	callMenu
	// callProbe is a reachability or uptime probe (watch loop, rediscovery,
	// doctor's clock check). It must not outlive its own watch tick.
	callProbe
	// callCapabilities is the startup/rediscovery capabilities fetch: a dark
	// clock must not delay boot.
	callCapabilities
	// callDoctor is `ember doctor`'s awtrix_reachable check, which also runs
	// offline against a bare config.
	callDoctor
)

const (
	menuCallTimeout         = 8 * time.Second
	probeCallTimeout        = 1500 * time.Millisecond
	capabilitiesCallTimeout = 2 * time.Second
	doctorFallbackTimeout   = 2 * time.Second
)

// timeout is the budget for one call of class c under cfg.
func (c callClass) timeout(cfg *Config) time.Duration {
	switch c {
	case callMenu:
		return menuCallTimeout
	case callProbe:
		return probeCallTimeout
	case callCapabilities:
		return capabilitiesCallTimeout
	case callDoctor:
		if t := time.Duration(cfg.AWTRIX.TimeoutSeconds) * time.Second; t > 0 {
			return t
		}
		return doctorFallbackTimeout
	}
	return time.Duration(cfg.AWTRIX.TimeoutSeconds) * time.Second
}

// errClockNotConfigured is every call's answer when there is no clock URL.
var errClockNotConfigured = errors.New("clock not configured")

// clockBaseURL is the clock cfg points at, trimmed and held to the same rule
// (validDeviceURL) every entry point applies when a URL is stored, so nothing
// but an absolute http(s) URL is ever dialled.
func clockBaseURL(cfg *Config) (string, error) {
	base := strings.TrimRight(cfg.AWTRIX.HTTPBaseURL, "/")
	if base == "" {
		return "", errClockNotConfigured
	}
	if err := validDeviceURL(base); err != nil {
		return "", err
	}
	return base, nil
}

// client returns an awtrix client for the currently-resolved clock with the
// class's timeout. The only awtrix.NewClient call in cmd/ember
// (clock_access_guard_test.go keeps it that way). Clients share
// http.DefaultTransport, so building one per call keeps the keep-alive pool.
func (k *clockAccess) client(c callClass) (*awtrix.Client, error) {
	cfg := k.cfg() // one load: URL and timeout come from the same config
	base, err := clockBaseURL(cfg)
	if err != nil {
		return nil, err
	}
	return awtrix.NewClient(base, c.timeout(cfg)), nil
}

// do runs one typed client call of class c against the clock.
func (k *clockAccess) do(ctx context.Context, c callClass, fn func(context.Context, *awtrix.Client) error) error {
	cl, err := k.client(c)
	if err != nil {
		return err
	}
	return fn(ctx, cl)
}

// deviceCall is one pass-through awtrix client call, usually a method
// expression such as (*awtrix.Client).RawSettings.
type deviceCall func(*awtrix.Client, context.Context) (awtrix.Reply, error)

// withBody binds a request body to a body-taking raw client call.
func withBody(call func(*awtrix.Client, context.Context, []byte) (awtrix.Reply, error), body []byte) deviceCall {
	return func(cl *awtrix.Client, ctx context.Context) (awtrix.Reply, error) {
		return call(cl, ctx, body)
	}
}

// raw runs a pass-through call (menu class) and returns the reply verbatim; a
// non-2xx status is not an error. For callers that give a status its own
// meaning (a 404 script is "absent").
func (k *clockAccess) raw(ctx context.Context, call deviceCall) (awtrix.Reply, error) {
	cl, err := k.client(callMenu)
	if err != nil {
		return awtrix.Reply{}, err
	}
	return call(cl, ctx)
}

// fetch is raw for callers that only want success: a non-2xx reply comes back
// as the clock's *awtrix.APIError, ready for writeClockError.
func (k *clockAccess) fetch(ctx context.Context, call deviceCall) ([]byte, error) {
	reply, err := k.raw(ctx, call)
	if err != nil {
		return nil, err
	}
	if reply.Status < 200 || reply.Status >= 300 {
		return nil, awtrix.ParseAPIError(reply.Status, reply.Body)
	}
	return reply.Body, nil
}

// reachable reports whether base answers as an awtrix-ng clock right now,
// within one probe budget. base is explicit because rediscovery probes the URL
// it is about to judge, not necessarily the one in the config.
func (k *clockAccess) reachable(ctx context.Context, base string) bool {
	_, ok := discovery.Reachable(ctx, &http.Client{Timeout: probeCallTimeout}, base)
	return ok
}

// readSystem fetches the clock's /api/v1/system object in full.
func (k *clockAccess) readSystem(ctx context.Context) (map[string]any, error) {
	body, err := k.fetch(ctx, (*awtrix.Client).RawSystem)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("clock /api/v1/system: %w", err)
	}
	return m, nil
}

// updateSystem read-merge-PUTs /api/v1/system: it reads the whole object,
// lets mutate change it, and writes all of it back, holding systemLock for
// the round trip so a concurrent writer can't read the object before this
// write lands. A caller whose ctx ends while waiting for the lock gets
// ctx.Err() without touching the clock. A full replace, never a partial PUT: a partial one that the firmware
// treated as a replace would drop the stored Wi-Fi password. NG applies system
// changes live, no reboot.
func (k *clockAccess) updateSystem(ctx context.Context, mutate func(sys map[string]any)) error {
	select {
	case k.systemLock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-k.systemLock }()
	sys, err := k.readSystem(ctx)
	if err != nil {
		return err
	}
	mutate(sys)
	payload, err := json.Marshal(sys)
	if err != nil {
		return err
	}
	_, err = k.fetch(ctx, withBody((*awtrix.Client).RawPutSystem, payload))
	return err
}

// ---- Error map ----

// writeClockError answers the menu for a failed clock call: a clock refusal
// (*awtrix.APIError) is relayed through writeDeviceAPIError, anything else (no
// clock configured, network failure, undecodable reply) is 502.
func writeClockError(w http.ResponseWriter, err error) {
	var apiErr *awtrix.APIError
	if errors.As(err, &apiErr) {
		writeDeviceAPIError(w, apiErr)
		return
	}
	writeError(w, http.StatusBadGateway, err)
}

// deviceProxyStatus picks the status the menu sees for a non-2xx clock reply.
// Request errors (bad value, unknown key, missing app, wrong media type) and
// a busy/absent-hardware 503 pass through unchanged, so the caller can tell
// "the clock refused this" from "the clock is broken or unreachable".
// Everything else becomes 502; a device 401/403 in particular must not read
// as the menu's own bearer token being wrong.
func deviceProxyStatus(status int) int {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict,
		http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity, http.StatusServiceUnavailable:
		return status
	}
	return http.StatusBadGateway
}

// writeDeviceError relays a non-2xx clock reply to the menu. The NG envelope
// ({"error":{code,message,field}}) is flattened into the server's own error
// shape — "error" stays a string, which is what the menu displays — with
// "code" and "field" alongside so a caller can point at the rejected key.
func writeDeviceError(w http.ResponseWriter, status int, body []byte) {
	writeDeviceAPIError(w, awtrix.ParseAPIError(status, body))
}

// writeDeviceAPIError is writeDeviceError for a reply the awtrix client has
// already parsed.
func writeDeviceAPIError(w http.ResponseWriter, apiErr *awtrix.APIError) {
	status := apiErr.StatusCode
	msg := fmt.Sprintf("clock returned %d", status)
	if detail := apiErr.Message; detail != "" {
		// Cap a raw (non-envelope) body at 200 runes, never mid-sequence.
		if r := []rune(detail); len(r) > 200 {
			detail = string(r[:200]) + "…"
		}
		msg += ": " + detail
	}
	if apiErr.Field != "" {
		msg += " (field " + apiErr.Field + ")"
	}
	out := map[string]string{"error": msg}
	if apiErr.Code != "" {
		out["code"] = apiErr.Code
	}
	if apiErr.Field != "" {
		out["field"] = apiErr.Field
	}
	writeJSON(w, deviceProxyStatus(status), out)
}

// ---- Retry ----

// publishAttemptTimeout bounds ONE pushed-app write, and publishAttempts is how
// many of them a frame gets before the coordinator gives up until the next tick.
//
// awtrix.timeout_seconds (10s by default) is the wrong budget here: it is the
// ceiling for any device call, while a frame push is a ~2.4 KB PUT to a device
// on the same LAN that answers in well under a second when the link is healthy
// (measured: 0.04s empty-ish, 0.55-0.68s at 3 KB). A push that has not answered
// in 2.5s has almost certainly been dropped, and every second spent waiting is a
// second the coordinator goroutine — which owns every device write — is not
// serving ticks, so its missed ticks turn into dropped state-change commands.
//
// Retrying inside the tick (rather than waiting a whole dwell for the next one)
// is what keeps a lossy link from evicting the app: the device drops a pushed
// app on its own lifetime, and it counts wallclock, not attempts.
const (
	publishAttemptTimeout = 2500 * time.Millisecond
	publishAttempts       = 2
)

// retryClockCall runs op up to publishAttempts times, each attempt bounded by
// budget, stopping early on success, on an answer a retry can't change (see
// retryableClockErr), or when ctx is done. onRetry runs before every attempt
// after the first. Returns the last attempt's error.
func retryClockCall(ctx context.Context, budget time.Duration, onRetry func(), op func(context.Context) error) error {
	var err error
	for i := 0; i < publishAttempts; i++ {
		if i > 0 {
			onRetry()
		}
		attemptCtx, cancel := context.WithTimeout(ctx, budget)
		err = op(attemptCtx)
		cancel()
		if err == nil || !retryableClockErr(err) || ctx.Err() != nil {
			return err
		}
	}
	return err
}

// retryableClockErr reports whether a failed clock call is worth another
// attempt. A transport failure (timeout, refused, reset) carries no status and
// is exactly the lost-packet case retries exist for. A device that answered
// has decided: only 5xx and 429 can change on their own — this clock
// watchdog-resets and runs its HTTP server on the same task that drives the
// panel, so a 503 while busy is transient. Any other 4xx (422 on a payload NG
// rejects, 413 on one too large) will answer identically forever.
func retryableClockErr(err error) bool {
	var apiErr *awtrix.APIError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.StatusCode >= 500 || apiErr.StatusCode == http.StatusTooManyRequests
}
