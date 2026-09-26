package main

import (
	"context"
	"encoding/json"
	"time"
)

// holdState is who owns the screen device-side, in precedence order.
type holdState int

const (
	// holdNone: the ember app takes its turn in the device's app loop like any
	// other tile, and no device setting is overridden. This covers the idle
	// frames too: their payload asks for a long dwell so they linger once the
	// rotation reaches them, but nothing about them is urgent enough to justify
	// pushing the clock's own apps off the screen.
	holdNone holdState = iota
	// holdAttention: a locked waiting/error frame needs the screen now, so the
	// app is force-switched to and its own long durationMs keeps it there for
	// the attention window.
	holdAttention
	// holdPomodoro: a running timer owns the screen for its whole phase, which
	// outlasts any dwell, so the device's rotation and native button navigation
	// are disabled for the duration.
	holdPomodoro
)

// exitRestoreBudget bounds the takeover restore on shutdown. It fits both
// retry attempts and stays inside main's shutdown wait.
const exitRestoreBudget = 5 * time.Second

// takeoverPriorKey is the store key holding the user's pre-takeover settings
// while a Pomodoro takeover is in force. Empty or absent means none is.
const takeoverPriorKey = "pomo_takeover_prior"

// settingsKV is the part of the SQLite store the coordinator persists the
// takeover snapshot in.
type settingsKV interface {
	GetSetting(key string) (value string, ok bool, err error)
	PutSetting(key, value string) error
}

// takeoverPrior is the user's own value of each device setting a Pomodoro
// takeover overrides. The restore writes these back instead of assuming the
// firmware defaults, which would clobber a choice made in the Device tab.
type takeoverPrior struct {
	AutoTransition  bool `json:"autoTransition"`
	BlockNavigation bool `json:"blockNavigation"`
}

// defaultTakeoverPrior is the firmware default, used when the device rejects
// the settings read outright.
func defaultTakeoverPrior() takeoverPrior { return takeoverPrior{AutoTransition: true} }

// priorFromSettings picks the two takeover keys out of GET /api/v1/settings,
// keeping the default for any key the device did not report as a bool.
func priorFromSettings(m map[string]any) takeoverPrior {
	p := defaultTakeoverPrior()
	if v, ok := m["autoTransition"].(bool); ok {
		p.AutoTransition = v
	}
	if v, ok := m["blockNavigation"].(bool); ok {
		p.BlockNavigation = v
	}
	return p
}

func (p takeoverPrior) settings() map[string]any {
	return map[string]any{"autoTransition": p.AutoTransition, "blockNavigation": p.BlockNavigation}
}

// takeoverOnSettings is what a Pomodoro takeover writes: rotation off, native
// button navigation blocked so the buttons drive the timer.
func takeoverOnSettings() map[string]any {
	return map[string]any{"autoTransition": false, "blockNavigation": true}
}

// settled reports whether a device call has an outcome worth committing: it
// succeeded, or the device answered with an error a retry cannot change. A
// transport failure or a 5xx is left for the next tick.
func settled(err error) bool {
	return err == nil || !retryablePushErr(err)
}

// setSettingsKV wires the store and picks up a takeover snapshot a previous
// process left behind (it died before its exit restore landed). With one
// loaded, the first publish that isn't a Pomodoro restores it. Call before Run.
func (c *coordinator) setSettingsKV(kv settingsKV) {
	c.kv = kv
	v, ok, err := kv.GetSetting(takeoverPriorKey)
	if err != nil {
		c.logger.Warn("load pomodoro takeover snapshot failed", "err", err)
		return
	}
	if !ok || v == "" {
		return
	}
	var p takeoverPrior
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		c.logger.Warn("discarding unreadable pomodoro takeover snapshot", "err", err)
		return
	}
	c.prior = &p
	c.logger.Info("pomodoro takeover left over from a previous run; restoring on the next publish",
		"autoTransition", p.AutoTransition, "blockNavigation", p.BlockNavigation)
}

// setPrior records (or, with nil, forgets) the takeover snapshot in memory
// and in the store. A failed store write only costs the crash recovery.
func (c *coordinator) setPrior(p *takeoverPrior) {
	c.prior = p
	if c.kv == nil {
		return
	}
	value := ""
	if p != nil {
		blob, err := json.Marshal(p)
		if err != nil {
			c.logger.Warn("encode pomodoro takeover snapshot failed", "err", err)
			return
		}
		value = string(blob)
	}
	if err := c.kv.PutSetting(takeoverPriorKey, value); err != nil {
		c.logger.Warn("persist pomodoro takeover snapshot failed", "err", err)
	}
}

// snapshotTakeoverPrior reads the user's current values before the takeover
// overwrites them. Returns false when the read was lost, so the takeover
// waits for the next tick rather than recording values the user may not have.
func (c *coordinator) snapshotTakeoverPrior(ctx context.Context) bool {
	var m map[string]any
	err := c.retryDevice(ctx, func(ctx context.Context) error {
		var err error
		m, err = c.publisher.ReadSettings(ctx)
		return err
	})
	if !settled(err) {
		c.logger.Warn("display hold settings snapshot failed; retrying next tick", "err", err)
		return false
	}
	p := defaultTakeoverPrior()
	if err != nil {
		c.logger.Warn("display hold settings snapshot rejected; restore will use firmware defaults", "err", err)
	} else {
		p = priorFromSettings(m)
	}
	c.setPrior(&p)
	return true
}

// restoreTakeover writes the snapshot back (firmware defaults if there is
// none) and forgets it. It returns the device error only when the write was
// lost and is worth retrying; the snapshot then stays for the retry.
func (c *coordinator) restoreTakeover(ctx context.Context) error {
	p := defaultTakeoverPrior()
	if c.prior != nil {
		p = *c.prior
	}
	err := c.retryDevice(ctx, func(ctx context.Context) error {
		return c.publisher.Settings(ctx, p.settings())
	})
	if !settled(err) {
		return err
	}
	if err != nil {
		c.logger.Warn("display hold restore settings rejected", "err", err)
	}
	c.setPrior(nil)
	return nil
}

// applyDisplayHold moves the device to the requested screen owner, writing only
// on the edge — a per-tick re-assert would spam apps/active and re-trigger the
// transition animation every dwell.
//
// awtrix-ng has no per-payload priority (AWTRIX3's prio+force 422 on NG), and a
// pushed app's own durationMs only takes effect once the rotation reaches its
// slot. So "this frame must own the screen NOW" is a forced PUT
// /api/v1/apps/active, which the app's long durationMs then sustains. Measured
// on firmware 1.0.13 against a live 7-app rotation: switch + durationMs=30000
// held 31 s; switch + durationMs=6000 held 6.7 s; so the switch supplies the
// jump and durationMs supplies the length.
//
// That is enough for a 30 s attention window but not for a 25-minute focus
// block, which is why holdPomodoro additionally sets autoTransition:false
// (verified to outrank the per-app dwell entirely) plus blockNavigation:true so
// the buttons drive the timer instead of the app loop. The user's own values
// of both are snapshotted (and persisted) first and written back on release.
// holdAttention deliberately leaves both settings alone: it stays crash-safe,
// since a server that dies mid-hold leaves the clock to expire the dwell and
// resume rotation on its own.
//
// c.hold only moves once every write for the edge has landed. About 44% of
// writes to this clock are lost, and a hold marked done after a lost write is
// never retried: the attention frame isn't forced, or the timer rotates away,
// or rotation stays off with no timer running. A lost write leaves c.hold
// where it was, so the next tick replays the edge. The writes are idempotent.
// A restore is pending whenever a snapshot exists, which also covers a
// takeover whose settings landed but whose switch did not, and one left by a
// previous process.
//
// Recovery from a device reboot (which drops the pushed apps) arrives as a
// cmdRepublish, which fakes the edge by resetting c.hold. Runs on the
// coordinator goroutine only.
func (c *coordinator) applyDisplayHold(want holdState, appName string) {
	restorePending := want != holdPomodoro && (c.hold == holdPomodoro || c.prior != nil)
	if want == c.hold && !restorePending {
		return
	}
	ctx := c.runCtx()
	if restorePending {
		if err := c.restoreTakeover(ctx); err != nil {
			c.logger.Warn("display hold restore settings failed; retrying next tick", "err", err)
			return
		}
		if c.hold == holdPomodoro {
			c.hold = holdNone
		}
		if want == c.hold {
			return
		}
	}
	if want == holdPomodoro {
		if c.prior == nil && !c.snapshotTakeoverPrior(ctx) {
			return
		}
		err := c.retryDevice(ctx, func(ctx context.Context) error {
			return c.publisher.Settings(ctx, takeoverOnSettings())
		})
		if !settled(err) {
			c.logger.Warn("display hold takeover settings failed; retrying next tick", "err", err)
			return
		}
		if err != nil {
			c.logger.Warn("display hold takeover settings rejected", "err", err)
		}
	}
	if want != holdNone {
		err := c.retryDevice(ctx, func(ctx context.Context) error {
			return c.publisher.Switch(ctx, appName)
		})
		if !settled(err) {
			c.logger.Warn("display hold switch failed; retrying next tick", "err", err, "hold", want)
			return
		}
		if err != nil {
			c.logger.Warn("display hold switch rejected", "err", err, "hold", want)
		}
	}
	c.hold = want
}

// restorePomoTakeoverOnExit puts the user's rotation + button-navigation
// settings back if a Pomodoro takeover may be in force when the coordinator
// stops. Uses a fresh context because the Run context is already cancelled on
// exit. If the device can't be reached the snapshot stays in the store and the
// next start restores it. A holdAttention needs no undo — nothing sticky was
// written for it.
func (c *coordinator) restorePomoTakeoverOnExit() {
	if c.hold != holdPomodoro && c.prior == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), exitRestoreBudget)
	defer cancel()
	if err := c.restoreTakeover(ctx); err != nil {
		c.logger.Warn("pomo restore on shutdown failed; next start will retry", "err", err)
		return
	}
	c.hold = holdNone
}
