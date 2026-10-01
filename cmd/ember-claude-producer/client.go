package main

import (
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

// Client, StatusRequest and DeleteRequest alias the internal/producer wire types.
type (
	StatusRequest = producer.StatusRequest
	DeleteRequest = producer.DeleteRequest
	Client        = producer.Client
)

// NewClient builds the hook-path client using HookTimeoutMs.
func NewClient(cfg Config) *Client {
	return producer.NewClient(cfg.ServerURL, cfg.Token, time.Duration(cfg.HookTimeoutMs)*time.Millisecond)
}

const daemonHTTPTimeout = 5 * time.Second

// NewDaemonClient builds the client for background daemon traffic, independent of HookTimeoutMs.
func NewDaemonClient(cfg Config) *Client {
	return producer.NewClient(cfg.ServerURL, cfg.Token, daemonHTTPTimeout).WithLinkStatus(daemonLink)
}

var daemonLink *producer.LinkStatus

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
