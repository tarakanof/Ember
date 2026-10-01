package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

type holdState int

const (
	holdNone holdState = iota
	holdAttention
	holdPomodoro
)

const exitRestoreBudget = 5 * time.Second

const restoreBackoffTicks = 5

const takeoverPriorKey = "pomo_takeover_prior"

type settingsKV interface {
	GetSetting(key string) (value string, ok bool, err error)
	PutSetting(key, value string) error
}

type takeoverPrior struct {
	AutoTransition  bool `json:"autoTransition"`
	BlockNavigation bool `json:"blockNavigation"`
}

func defaultTakeoverPrior() takeoverPrior { return takeoverPrior{AutoTransition: true} }

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

func takeoverOnSettings() map[string]any {
	return map[string]any{"autoTransition": false, "blockNavigation": true}
}

func settled(err error) bool {
	return err == nil || !retryableClockErr(err)
}

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

func (c *coordinator) setPrior(p *takeoverPrior) {
	c.prior = p
	c.priorGen++
	c.persistPrior()
}

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

var takeoverKeys = []string{"autoTransition", "blockNavigation"}

func (c *coordinator) applyMenuSettings(ctx context.Context, m map[string]any, write func(map[string]any) error) ([]string, error) {
	var held []string
	for _, k := range takeoverKeys {
		if _, ok := m[k]; ok {
			held = append(held, k)
		}
	}
	if len(held) == 0 {
		return nil, write(m)
	}
	if err := c.priorMu.LockContext(ctx); err != nil {
		return nil, err
	}
	c.editSeq++
	seq := c.editSeq
	if c.prior == nil {
		gen := c.priorGen
		c.priorMu.Unlock()
		if err := write(m); err != nil {
			return nil, err
		}
		if err := c.priorMu.LockContext(ctx); err != nil {
			return nil, err
		}
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

func (c *coordinator) takeoverPriorViewContext(ctx context.Context) (takeoverPrior, bool, error) {
	if err := c.priorMu.LockContext(ctx); err != nil {
		return takeoverPrior{}, false, err
	}
	defer c.priorMu.Unlock()
	if c.prior == nil {
		return takeoverPrior{}, false, nil
	}
	return *c.prior, true, nil
}

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
