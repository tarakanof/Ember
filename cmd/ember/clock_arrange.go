package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

const clockArrangeRetry = 30 * time.Second

type clockArranger struct {
	mu      sync.Mutex
	now     func() time.Time
	base    string
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

func (g *clockArranger) want(base, app string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if base != g.base || g.settled == nil {
		g.base = base
		g.settled = map[string]bool{}
		g.pending = map[string]bool{}
		g.retryAt = time.Time{}
	}
	if g.settled[app] {
		return false
	}
	g.pending[app] = true
	if g.running || g.clock().Before(g.retryAt) {
		return false
	}
	g.running = true
	return true
}

func (g *clockArranger) take() (base string, names []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	names = slices.Sorted(maps.Keys(g.pending))
	if len(names) == 0 {
		g.running = false
		return "", nil
	}
	clear(g.pending)
	return g.base, names
}

func (g *clockArranger) done(base string, names, settled []string, failed bool) (more bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if base != g.base || g.settled == nil {
		return true
	}
	for _, n := range settled {
		g.settled[n] = true
	}
	if !failed {
		return true
	}
	for _, n := range names {
		if !g.settled[n] {
			g.pending[n] = true
		}
	}
	g.retryAt = g.clock().Add(clockArrangeRetry)
	g.running = false
	return false
}

func (g *clockArranger) forget() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.settled = nil
	g.pending = nil
	g.retryAt = time.Time{}
}

func (a *App) arrangeClockApp(app string) {
	if clockDisabled() {
		return
	}
	if a.arranger.want(a.cfg.Load().effectiveClockURL(), app) {
		a.clockJobs.Go(a.runClockArrange)
	}
}

func (a *App) runClockArrange(ctx context.Context) {
	for {
		base, names := a.arranger.take()
		if len(names) == 0 {
			return
		}
		settled, err := a.arrangeClockApps(ctx, names)
		if err != nil {
			a.clockJobFailed(ctx, "clock app order not arranged", "apps", names, "err", err)
		}
		if !a.arranger.done(base, names, settled, err != nil) {
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
	payload, _ := json.Marshal(plan.order)
	if _, err := a.clock.fetch(ctx, withBody((*awtrix.Client).RawPutAppOrder, payload)); err != nil {
		a.forgetClockRotationAfter(err)
		return nil, err
	}
	a.logger.InfoContext(ctx, "clock app order: Ember apps given a slot", "apps", plan.placed)
	a.setClockRotation(a.readBackClockRotation(ctx))
	return plan.settled, nil
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
	order   deviceAppsPutBody
	placed  []string
	settled []string
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
	for _, r := range rows {
		if r.Origin == "module" {
			continue
		}
		enabled := r.Enabled == nil || *r.Enabled
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
