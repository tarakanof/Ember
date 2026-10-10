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

type clockAccess struct {
	cfg     func() *Config
	connect func(base string, timeout time.Duration) *awtrix.Client

	systemLock ctxLock

	writeBudget time.Duration
	readBudget  time.Duration
}

func newClockAccess(cfg func() *Config) *clockAccess {
	return &clockAccess{cfg: cfg, connect: awtrix.NewClient, systemLock: newCtxLock(), writeBudget: clockWriteBudget, readBudget: clockReadBudget}
}

type ctxLock chan struct{}

func newCtxLock() ctxLock { return make(ctxLock, 1) }

func (l ctxLock) Lock() { l <- struct{}{} }

func (l ctxLock) Unlock() {
	select {
	case <-l:
	default:
		panic("ctxLock: unlock of unlocked lock")
	}
}

func (l ctxLock) LockContext(ctx context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type callClass int

const (
	callPublish callClass = iota
	callMenu
	callProbe
	callCapabilities
	callDoctor
)

const (
	menuCallTimeout         = 8 * time.Second
	probeCallTimeout        = 1500 * time.Millisecond
	capabilitiesCallTimeout = 2 * time.Second
	doctorFallbackTimeout   = 2 * time.Second
)

const clockWriteBudget = 25 * time.Second

const clockReadBudget = 25 * time.Second

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

var errClockNotConfigured = errors.New("clock not configured")

var errClockDisabled = errors.New("clock disabled (EMBER_CLOCK=off)")

func clockBaseURL(cfg *Config) (string, error) {
	base := strings.TrimRight(cfg.effectiveClockURL(), "/")
	if base == "" {
		return "", errClockNotConfigured
	}
	if err := validDeviceURL(base); err != nil {
		return "", err
	}
	return base, nil
}

func (k *clockAccess) client(c callClass) (*awtrix.Client, error) {
	if clockDisabled() {
		return nil, errClockDisabled
	}
	cfg := k.cfg()
	base, err := clockBaseURL(cfg)
	if err != nil {
		return nil, err
	}
	return k.connect(base, c.timeout(cfg)), nil
}

func (k *clockAccess) do(ctx context.Context, c callClass, fn func(context.Context, *awtrix.Client) error) error {
	cl, err := k.client(c)
	if err != nil {
		return err
	}
	return fn(ctx, cl)
}

type deviceCall func(*awtrix.Client, context.Context) (awtrix.Reply, error)

func withBody(call func(*awtrix.Client, context.Context, []byte) (awtrix.Reply, error), body []byte) deviceCall {
	return func(cl *awtrix.Client, ctx context.Context) (awtrix.Reply, error) {
		return call(cl, ctx, body)
	}
}

func (k *clockAccess) raw(ctx context.Context, call deviceCall) (awtrix.Reply, error) {
	cl, err := k.client(callMenu)
	if err != nil {
		return awtrix.Reply{}, err
	}
	return call(cl, ctx)
}

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

func (k *clockAccess) reachable(ctx context.Context, base string) bool {
	if clockDisabled() {
		return false
	}
	_, ok := discovery.Reachable(ctx, &http.Client{Timeout: probeCallTimeout}, base)
	return ok
}

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

func (k *clockAccess) updateSystem(ctx context.Context, mutate func(sys map[string]any)) (map[string]any, error) {
	if err := k.systemLock.LockContext(ctx); err != nil {
		return nil, err
	}
	defer k.systemLock.Unlock()
	sys, err := k.readSystem(ctx)
	if err != nil {
		return nil, err
	}
	mutate(sys)
	payload, err := json.Marshal(sys)
	if err != nil {
		return nil, err
	}
	if err := k.sendWrite(ctx, withBody((*awtrix.Client).RawPutSystem, payload)); err != nil {
		return nil, err
	}
	return sys, nil
}

func (k *clockAccess) sendWrite(ctx context.Context, call deviceCall) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := k.fetch(ctx, call); err != nil {
		return sentWriteError{err}
	}
	return nil
}

func (k *clockAccess) writeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, k.writeBudget)
}

func (k *clockAccess) readContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, k.readBudget)
}

type sentWriteError struct{ err error }

func (e sentWriteError) Error() string { return e.err.Error() }
func (e sentWriteError) Unwrap() error { return e.err }

type writeOutcome string

const (
	writeNotSent writeOutcome = "not_sent"
	writeUnknown writeOutcome = "unknown"
	writeApplied writeOutcome = "applied"
)

func (k *clockAccess) writeBudgetError(ctx context.Context, w http.ResponseWriter, err error, landed bool) {
	var apiErr *awtrix.APIError
	if errors.As(err, &apiErr) || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeClockError(w, err)
		return
	}
	outcome, fate := writeNotSent, "nothing was changed"
	var sent sentWriteError
	switch {
	case errors.As(err, &sent):
		outcome, fate = writeUnknown, "the change may not have been saved"
	case landed:
		outcome, fate = writeApplied, "the change was saved but not fully applied"
	}
	writeJSON(w, http.StatusGatewayTimeout, map[string]string{
		"error": fmt.Sprintf("clock didn't finish within %s: %s", k.writeBudget, fate),
		"code":  "clock_timeout",
		"write": string(outcome),
	})
}

func (k *clockAccess) readBudgetError(ctx context.Context, w http.ResponseWriter, err error) {
	var apiErr *awtrix.APIError
	if errors.As(err, &apiErr) || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeClockError(w, err)
		return
	}
	writeJSON(w, http.StatusGatewayTimeout, map[string]string{
		"error": fmt.Sprintf("clock didn't finish within %s", k.readBudget),
		"code":  "clock_timeout",
	})
}

func writeClockError(w http.ResponseWriter, err error) {
	var apiErr *awtrix.APIError
	if errors.As(err, &apiErr) {
		writeDeviceAPIError(w, apiErr)
		return
	}
	writeError(w, http.StatusBadGateway, err)
}

func deviceProxyStatus(status int) int {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict,
		http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity, http.StatusServiceUnavailable:
		return status
	}
	return http.StatusBadGateway
}

func writeDeviceError(w http.ResponseWriter, status int, body []byte) {
	writeDeviceAPIError(w, awtrix.ParseAPIError(status, body))
}

func writeDeviceAPIError(w http.ResponseWriter, apiErr *awtrix.APIError) {
	status := apiErr.StatusCode
	msg := fmt.Sprintf("clock returned %d", status)
	if detail := apiErr.Message; detail != "" {
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

const (
	publishAttemptTimeout = 2500 * time.Millisecond
	publishAttempts       = 2
)

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

func retryableClockErr(err error) bool {
	var apiErr *awtrix.APIError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.StatusCode >= 500 || apiErr.StatusCode == http.StatusTooManyRequests
}
