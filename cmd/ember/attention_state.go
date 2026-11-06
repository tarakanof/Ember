package main

import (
	"slices"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

type attentionStep int

const (
	attentionKeep attentionStep = iota
	attentionAcquire
	attentionRenew
	attentionDrain
)

type attentionEnd string

const (
	attentionHeld          attentionEnd = ""
	attentionEndDrain      attentionEnd = "drain"
	attentionEndReap       attentionEnd = "reap"
	attentionEndAckTimeout attentionEnd = "ack_timeout"
)

type attentionState struct {
	locked        bool
	lockedKey     string
	lockEnteredAt time.Time
}

func isAttentionState(state string) bool { return state == "waiting" || state == "error" }

func (a attentionState) holds(key string) bool { return a.locked && a.lockedKey == key }

func (a attentionState) onTransition(key, prior, next string) attentionStep {
	in, was := isAttentionState(next), isAttentionState(prior)
	switch {
	case in && prior != next && !was:
		return attentionAcquire
	case in && prior != next && was && a.holds(key):
		return attentionRenew
	case !in && a.holds(key):
		return attentionDrain
	}
	return attentionKeep
}

func (a attentionState) ended(active []string, sessions []render.Session, now time.Time, ackTimeout time.Duration) attentionEnd {
	if !a.locked {
		return attentionHeld
	}
	if !slices.Contains(active, a.lockedKey) {
		return attentionEndReap
	}
	for _, s := range sessions {
		if s.Key() == a.lockedKey {
			if !isAttentionState(s.State) {
				return attentionEndDrain
			}
			break
		}
	}
	if now.Sub(a.lockEnteredAt) >= ackTimeout {
		return attentionEndAckTimeout
	}
	return attentionHeld
}

func (a *attentionState) acquire(key string, now time.Time) {
	a.locked, a.lockedKey, a.lockEnteredAt = true, key, now
}

func (a *attentionState) renew(now time.Time) { a.lockEnteredAt = now }

func (a *attentionState) release() { a.locked, a.lockedKey = false, "" }
