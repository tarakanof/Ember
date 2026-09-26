package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
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

// restoreBackoffTicks is how many publishes a pending restore sits out after
// one was lost. The restore is owed even when the frame is nil or deduped, so
// with the clock offline every tick would otherwise block the (single)
// coordinator goroutine for a full retry budget, delaying attention upserts.
const restoreBackoffTicks = 5

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
//
// A reading equal to the takeover itself is treated as unknown: the clock is
// most likely still in a takeover nobody restored (an older binary that died
// mid-focus, a lost snapshot write), and recording it as the user's choice
// would make every later restore turn rotation off for good.
func priorFromSettings(m map[string]any) takeoverPrior {
	p := defaultTakeoverPrior()
	if v, ok := m["autoTransition"].(bool); ok {
		p.AutoTransition = v
	}
	if v, ok := m["blockNavigation"].(bool); ok {
		p.BlockNavigation = v
	}
	if !p.AutoTransition && p.BlockNavigation {
		return defaultTakeoverPrior()
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
	return err == nil || !retryableClockErr(err)
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
	c.priorMu.Lock()
	c.prior = &p
	c.priorMu.Unlock()
	c.logger.Info("pomodoro takeover left over from a previous run; restoring on the next publish",
		"autoTransition", p.AutoTransition, "blockNavigation", p.BlockNavigation)
}

// setPrior records (or, with nil, forgets) the takeover snapshot in memory
// and in the store. The caller holds priorMu.
func (c *coordinator) setPrior(p *takeoverPrior) {
	c.prior = p
	c.priorGen++
	c.persistPrior()
}

// persistPrior mirrors c.prior into the store. A failed store write only
// costs the crash recovery. The caller holds priorMu.
func (c *coordinator) persistPrior() {
	if c.kv == nil {
		return
	}
	value := ""
	if c.prior != nil {
		blob, err := json.Marshal(c.prior)
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
	c.priorMu.Lock()
	defer c.priorMu.Unlock()
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
	c.priorMu.Lock()
	defer c.priorMu.Unlock()
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

// takeoverKeys are the device settings a Pomodoro takeover overrides, in the
// order applyMenuSettings reports them.
var takeoverKeys = []string{"autoTransition", "blockNavigation"}

// applyMenuSettings writes a menu edit of the device settings (already
// validated) through write. While a takeover snapshot exists, the takeover
// keys are not written to the device: that would resume rotation or unblock
// the buttons mid-focus, and the restore would then overwrite them anyway.
// They go into the snapshot instead (and its stored copy, for a crash), so
// the restore applies them when the focus block ends. The other keys are
// written as usual, first, and the snapshot changes only if that write
// succeeded, so a failed save changes nothing. Returns the keys it held back.
//
// It runs on an HTTP goroutine and takes priorMu only for an edit that
// touches a takeover key. With no snapshot, the edit is written unlocked;
// if a takeover edge ran during that write (priorGen moved), it is folded
// in afterwards (see reconcileRacedEdit). Each such edit takes a sequence
// number on entry, so a slow older edit can't replace a newer one's value.
//
// An error means the edit may be partly applied: a raced edit whose first
// write landed but whose re-write over a restore was lost answers the error
// (502 for a lost write) although the clock briefly held the edit. The menu
// shows the save as failed and re-reads, which reports the clock's state.
func (c *coordinator) applyMenuSettings(m map[string]any, write func(map[string]any) error) ([]string, error) {
	var held []string
	for _, k := range takeoverKeys {
		if _, ok := m[k]; ok {
			held = append(held, k)
		}
	}
	if len(held) == 0 {
		return nil, write(m)
	}
	c.priorMu.Lock()
	c.editSeq++
	seq := c.editSeq
	if c.prior == nil {
		gen := c.priorGen
		c.priorMu.Unlock()
		if err := write(m); err != nil {
			return nil, err
		}
		c.priorMu.Lock()
		defer c.priorMu.Unlock()
		newer := c.claimKeys(m, held, seq)
		if c.priorGen == gen {
			return nil, nil
		}
		return c.reconcileRacedEdit(m, held, newer, write)
	}
	defer c.priorMu.Unlock()
	rest := make(map[string]any, len(m))
	for k, v := range m {
		rest[k] = v
	}
	for _, k := range held {
		delete(rest, k)
	}
	if len(rest) > 0 {
		if err := write(rest); err != nil {
			return nil, err
		}
	}
	c.mergeIntoPrior(m, c.claimKeys(m, held, seq))
	return held, nil
}

// claimKeys returns the keys of held for which edit seq (with values m) is
// the newest edit to have landed, and records it and its value as such. The
// caller holds priorMu.
func (c *coordinator) claimKeys(m map[string]any, held []string, seq uint64) []string {
	if c.keySeq == nil {
		c.keySeq, c.keyVal = map[string]uint64{}, map[string]any{}
	}
	var newer []string
	for _, k := range held {
		if seq > c.keySeq[k] {
			c.keySeq[k], c.keyVal[k] = seq, m[k]
			newer = append(newer, k)
		}
	}
	return newer
}

// reconcileRacedEdit handles an edit whose unlocked device write overlapped
// a takeover edge. newer is the part of held no later edit has already set.
// If a snapshot now exists, its read may predate the edit, so the newer keys
// go into it, and the takeover's values are written back for all edited keys
// in case the edit landed after the takeover's own write. If none exists, a
// whole takeover (snapshot and restore) ran during the write, and whichever
// of the restore and the edit landed last may be stale, so each edited key
// is written again with its newest value: this edit's, or a later edit's
// that the restore applied. The caller holds priorMu.
func (c *coordinator) reconcileRacedEdit(m map[string]any, held, newer []string, write func(map[string]any) error) ([]string, error) {
	if c.prior == nil {
		again := make(map[string]any, len(held))
		for _, k := range held {
			again[k] = c.keyVal[k]
		}
		return nil, write(again)
	}
	c.mergeIntoPrior(m, newer)
	on := takeoverOnSettings()
	reassert := make(map[string]any, len(held))
	for _, k := range held {
		reassert[k] = on[k]
	}
	if err := write(reassert); err != nil {
		c.logger.Warn("re-asserting pomodoro takeover after a racing menu edit failed; the edit may show mid-focus",
			"err", err)
	}
	return held, nil
}

// mergeIntoPrior copies the given takeover keys of the edit into the
// snapshot and persists it. The caller holds priorMu and has checked c.prior
// is non-nil.
func (c *coordinator) mergeIntoPrior(m map[string]any, keys []string) {
	if len(keys) == 0 {
		return
	}
	for _, k := range keys {
		v, _ := m[k].(bool)
		switch k {
		case "autoTransition":
			c.prior.AutoTransition = v
		case "blockNavigation":
			c.prior.BlockNavigation = v
		}
	}
	c.persistPrior()
	c.logger.Info("pomodoro takeover in force; menu edit applies when it ends",
		"keys", keys, "autoTransition", c.prior.AutoTransition, "blockNavigation", c.prior.BlockNavigation)
}

// takeoverPriorView returns the user's own takeover-key values while a
// takeover snapshot exists, for the menu to show instead of the takeover's.
func (c *coordinator) takeoverPriorView() (takeoverPrior, bool) {
	c.priorMu.Lock()
	defer c.priorMu.Unlock()
	if c.prior == nil {
		return takeoverPrior{}, false
	}
	return *c.prior, true
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
		if c.restoreBackoff > 0 {
			c.restoreBackoff--
			return
		}
		if err := c.restoreTakeover(ctx); err != nil {
			c.restoreBackoff = restoreBackoffTicks
			c.logger.Warn("display hold restore settings failed; retrying after backoff",
				"err", err, "skip_ticks", restoreBackoffTicks)
			return
		}
		c.restoreBackoff = 0
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
		// Attention skips the transition (NG fast:true): an agent waiting on
		// the user should be on screen now, not after a ~1 s animation. A
		// Pomodoro start was asked for by a button press, so it keeps it.
		mode := awtrix.SwitchAnimated
		if want == holdAttention {
			mode = awtrix.SwitchInstant
		}
		err := c.retryDevice(ctx, func(ctx context.Context) error {
			return c.publisher.Switch(ctx, appName, mode)
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
