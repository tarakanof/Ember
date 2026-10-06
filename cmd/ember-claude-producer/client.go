package main

import (
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type (
	StatusRequest = producer.StatusRequest
	DeleteRequest = producer.DeleteRequest
	Client        = producer.Client
)

func NewClient(cfg Config) *Client {
	return producer.NewClient(cfg.ServerURL, cfg.Token, time.Duration(cfg.HookTimeoutMs)*time.Millisecond)
}

const daemonHTTPTimeout = 5 * time.Second

func NewDaemonClient(cfg Config) *Client {
	c := producer.NewClient(cfg.ServerURL, cfg.Token, daemonHTTPTimeout).WithLinkStatus(daemonLink)
	if cfg.ServerAuto {
		c.WithAutoServer(daemonServer)
	}
	return c
}

var daemonLink *producer.LinkStatus

var daemonServer *producer.AutoServer

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
