package main

import (
	"time"

	"github.com/dt/awtrix-ai-status/internal/producer"
)

// The wire client + protocol types live in internal/producer. These aliases
// keep the existing call sites in this package unchanged.
type (
	StatusRequest = producer.StatusRequest
	DeleteRequest = producer.DeleteRequest
	Client        = producer.Client
)

// NewClient adapts the Claude Config to the shared constructor.
func NewClient(cfg Config) *Client {
	return producer.NewClient(cfg.ServerURL, cfg.Token, time.Duration(cfg.HookTimeoutMs)*time.Millisecond)
}
