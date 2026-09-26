package main

import (
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

// The wire client + protocol types live in internal/producer. These aliases
// keep the existing call sites in this package unchanged.
type (
	StatusRequest = producer.StatusRequest
	DeleteRequest = producer.DeleteRequest
	Client        = producer.Client
)

// NewClient adapts the Claude Config to the shared constructor, using
// HookTimeoutMs. Only for the hook path: it blocks the `claude` CLI, so the
// timeout must stay short (default 500ms).
func NewClient(cfg Config) *Client {
	return producer.NewClient(cfg.ServerURL, cfg.Token, time.Duration(cfg.HookTimeoutMs)*time.Millisecond)
}

// daemonHTTPTimeout bounds background daemon traffic (heartbeat re-POSTs,
// reap DELETEs). It is deliberately decoupled from HookTimeoutMs: the daemon
// isn't blocking an interactive CLI, so it can afford to wait out a slower
// link instead of flapping every 500ms.
const daemonHTTPTimeout = 5 * time.Second

// NewDaemonClient adapts the Claude Config to the shared constructor for
// background daemon traffic (heartbeat/usage), independent of HookTimeoutMs.
func NewDaemonClient(cfg Config) *Client {
	return producer.NewClient(cfg.ServerURL, cfg.Token, daemonHTTPTimeout).WithLinkStatus(daemonLink)
}

// daemonLink records whether the LaunchAgent reaches the server, for the
// app's Local Network hint. Set only by runDaemon: the one-shot `tick` runs
// in a terminal, whose network permission says nothing about the agent's.
var daemonLink *producer.LinkStatus

// wireRequest is the one POST /v1/status body builder for the hook and
// heartbeat paths. It turns a marker's stored request into a POST /v1/status body
// under the current config: the card/bar toggles come
// from config, context_pct only when enabled, and the marker-only weekly
// fields are stripped.
func wireRequest(cfg Config, req StatusRequest) StatusRequest {
	if !cfg.ContextPctEnabled {
		req.ContextPct = nil
	}
	sc, sb := cfg.SourceCardEnabled, cfg.SessionBarEnabled
	req.SourceCard, req.SessionBar = &sc, &sb
	req.RateWeekPct = nil
	req.RateWeekResetAt = 0
	req.RateWeekResetLabel = ""
	return req
}
