package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

const (
	clockArrangeRetry       = 30 * time.Second
	clockArrangeScriptsWait = 5 * time.Minute
)

var errClockScriptingStopped = errors.New("clock scripting is stopped: its scripts read as off")

type clockArranger struct {
	mu      sync.Mutex
	now     func() time.Time
	base    string
	gen     uint64
	settled map[string]bool
	pending map[string]bool
	running bool
	retryAt time.Time
}

func (g *clockArranger) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *clockArranger) resetLocked(base string) {
	g.base = base
	g.gen++
	g.settled = map[string]bool{}
	g.pending = map[string]bool{}
	g.retryAt = time.Time{}
}

func (g *clockArranger) startLocked() bool {
	if len(g.pending) == 0 || g.running || g.clock().Before(g.retryAt) {
		return false
	}
	g.running = true
	return true
}

func (g *clockArranger) want(base, app string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if base != g.base || g.settled == nil {
		g.resetLocked(base)
	}
	if !g.settled[app] {
		g.pending[app] = true
	}
	return g.startLocked()
}

func (g *clockArranger) recheck() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.settled == nil {
		return false
	}
	for n := range g.settled {
		g.pending[n] = true
	}
	clear(g.settled)
	g.retryAt = time.Time{}
	return g.startLocked()
}

func (g *clockArranger) take() (gen uint64, names []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	names = slices.Sorted(maps.Keys(g.pending))
	if len(names) == 0 {
		g.running = false
		return g.gen, nil
	}
	clear(g.pending)
	return g.gen, names
}

func (g *clockArranger) done(gen uint64, names, settled []string, retry time.Duration) (more bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if gen != g.gen {
		return true
	}
	for _, n := range settled {
		g.settled[n] = true
	}
	if retry <= 0 {
		return true
	}
	for _, n := range names {
		if !g.settled[n] {
			g.pending[n] = true
		}
	}
	g.retryAt = g.clock().Add(retry)
	g.running = false
	return false
}

func (g *clockArranger) forget() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resetLocked(g.base)
}

func (a *App) arrangeClockApp(app string) {
	if clockDisabled() {
		return
	}
	if a.arranger.want(a.cfg.Load().effectiveClockURL(), app) {
		a.clockJobs.Go(a.runClockArrange)
	}
}

func (a *App) recheckClockApps() {
	if clockDisabled() {
		return
	}
	if a.arranger.recheck() {
		a.clockJobs.Go(a.runClockArrange)
	}
}

func (a *App) runClockArrange(ctx context.Context) {
	for {
		gen, names := a.arranger.take()
		if len(names) == 0 {
			return
		}
		settled, err := a.arrangeClockApps(ctx, names)
		var retry time.Duration
		switch {
		case errors.Is(err, errClockScriptingStopped):
			a.logger.DebugContext(ctx, "clock app order not arranged", "apps", names, "err", err)
			retry = clockArrangeScriptsWait
		case err != nil:
			a.clockJobFailed(ctx, "clock app order not arranged", "apps", names, "err", err)
			retry = clockArrangeRetry
		}
		if !a.arranger.done(gen, names, settled, retry) {
			return
		}
	}
}

func (a *App) arrangeClockApps(ctx context.Context, names []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, clockRotationOpBudget)
	defer cancel()
	select {
	case a.rotationOp.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-a.rotationOp.slot }()
	body, err := a.clock.fetch(ctx, (*awtrix.Client).RawApps)
	if err != nil {
		return nil, err
	}
	var rows []clockAppRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode clock apps: %w", err)
	}
	plan := arrangeEmberApps(rows, names, emberAppRanks(a.cfg.Load().AWTRIX.AppName))
	if len(plan.placed) == 0 {
		return plan.settled, nil
	}
	if plan.offScripts {
		if err := a.clockScriptingRunning(ctx); err != nil {
			return nil, err
		}
	}
	payload, _ := json.Marshal(plan.order)
	if _, err := a.clock.fetch(ctx, withBody((*awtrix.Client).RawPutAppOrder, payload)); err != nil {
		a.forgetClockRotationAfter(err)
		return nil, err
	}
	a.logger.InfoContext(ctx, "clock app order: Ember apps given a slot", "apps", plan.placed)
	a.setClockRotation(a.readBackClockRotation(ctx))
	return plan.settled, nil
}

func (a *App) clockScriptingRunning(ctx context.Context) error {
	body, err := a.clock.fetch(ctx, (*awtrix.Client).RawDevice)
	if err != nil {
		return err
	}
	var dev struct {
		ScriptingRunning *bool `json:"scriptingRunning"`
	}
	if err := json.Unmarshal(body, &dev); err != nil {
		return fmt.Errorf("decode clock device: %w", err)
	}
	if dev.ScriptingRunning == nil || !*dev.ScriptingRunning {
		return errClockScriptingStopped
	}
	return nil
}

func emberAppRanks(baseApp string) map[string]int {
	ranks := make(map[string]int, len(tiles)+1)
	ranks[baseApp] = 0
	for i, t := range tiles {
		if _, dup := ranks[t.app]; !dup {
			ranks[t.app] = i + 1
		}
	}
	return ranks
}

type emberArrangement struct {
	order      deviceAppsPutBody
	placed     []string
	settled    []string
	offScripts bool
}

func arrangeEmberApps(rows []clockAppRow, names []string, ranks map[string]int) emberArrangement {
	type slotted struct {
		slot int
		name string
	}
	var arranged []slotted
	listed := map[string]bool{}
	hasSlot := map[string]bool{}
	on := map[string]bool{}
	disabled := []string{}
	offScripts := false
	for _, r := range rows {
		if r.Origin == "module" {
			continue
		}
		enabled := r.Enabled == nil || *r.Enabled
		offScripts = offScripts || (r.Origin == "script" && !enabled)
		if !listed[r.Name] {
			listed[r.Name] = true
			on[r.Name] = enabled
			if !enabled {
				disabled = append(disabled, r.Name)
			}
		}
		if r.Slot != nil {
			arranged = append(arranged, slotted{*r.Slot, r.Name})
			hasSlot[r.Name] = true
		}
	}
	slices.SortStableFunc(arranged, func(x, y slotted) int { return x.slot - y.slot })
	order := make([]string, 0, len(arranged)+len(names))
	for _, s := range arranged {
		order = append(order, s.name)
	}
	stored := len(order) > 0
	want := slices.Clone(names)
	slices.SortStableFunc(want, func(x, y string) int { return rankOf(ranks, x) - rankOf(ranks, y) })
	var out emberArrangement
	for _, name := range want {
		if !listed[name] {
			continue
		}
		out.settled = append(out.settled, name)
		if !stored || hasSlot[name] || !on[name] {
			continue
		}
		order = slices.Insert(order, emberSlot(order, name, ranks), name)
		hasSlot[name] = true
		out.placed = append(out.placed, name)
	}
	out.order = deviceAppsPutBody{Order: order, Disabled: disabled}
	out.offScripts = offScripts
	return out
}

func rankOf(ranks map[string]int, name string) int {
	if r, ok := ranks[name]; ok {
		return r
	}
	return len(ranks)
}

func emberSlot(order []string, name string, ranks map[string]int) int {
	r := rankOf(ranks, name)
	after, before := -1, -1
	for i, n := range order {
		k, ok := ranks[n]
		switch {
		case !ok:
		case k <= r:
			after = i
		case before < 0:
			before = i
		}
	}
	switch {
	case after >= 0:
		return after + 1
	case before >= 0:
		return before
	}
	return len(order)
}
