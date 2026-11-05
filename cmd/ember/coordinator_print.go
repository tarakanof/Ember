package main

import (
	"context"
	"math"
	"time"

	"github.com/tarakanof/ember/internal/printer"
	"github.com/tarakanof/ember/internal/render"
)

const (
	printChangeSoon       = 2 * time.Minute
	printDonePopupSec     = 10
	printAttentionPopSec  = 120
	printErrorPopupSec    = 30
	printAttentionRepeat  = 10 * time.Minute
	printDoneGrace        = 5 * time.Minute
	printNoticeCallBudget = 10 * time.Second
	printRetryAfter       = 30 * time.Second
	printHeatStep         = 5

	printDoneRTTTL      = "done:d=8,o=6,b=160:c,e,g,2c7"
	printAttentionRTTTL = "attn:d=8,o=6,b=200:a,p,a"
	printErrorRTTTL     = "err:d=4,o=5,b=140:c,p,c"
)

func printInRotation(s printer.Snapshot) bool {
	if !s.Enabled {
		return false
	}
	switch s.Phase {
	case printer.PhaseHeating, printer.PhasePreparing, printer.PhasePrinting, printer.PhaseFinishing,
		printer.PhasePaused, printer.PhaseAttention:
		return true
	case printer.PhaseFinished, printer.PhaseStopped:
		return !s.HoldUntil.IsZero()
	}
	return false
}

func printCounting(s printer.Snapshot) bool {
	return s.Phase == printer.PhasePrinting || s.Phase == printer.PhasePreparing
}

func printViewOf(s printer.Snapshot, now time.Time) render.PrintView {
	v := render.PrintView{
		Phase:        string(s.Phase),
		Progress:     s.Progress,
		Nozzle:       stepDegrees(s.Nozzle),
		NozzleTarget: int(math.Round(s.NozzleTarget)),
		Bed:          stepDegrees(s.Bed),
		BedTarget:    int(math.Round(s.BedTarget)),
	}
	if !s.EndsAt.IsZero() {
		v.HaveLeft = true
		v.LeftSec = max(0, int(s.EndsAt.Sub(now).Seconds()))
		v.ReadyAt = s.EndsAt.In(now.Location()).Format("15:04")
	}
	if !s.ChangeAt.IsZero() && s.ChangeAt.Sub(now) <= printChangeSoon {
		v.ChangeSoon = true
	}
	return v
}

func stepDegrees(v float64) int {
	return int(math.Round(v/printHeatStep)) * printHeatStep
}

func printTileOf(card int, needEstimate bool) func(in *tileInputs) (tileView, bool) {
	return func(in *tileInputs) (tileView, bool) {
		v := printViewOf(in.print, in.now)
		if needEstimate && !v.HaveLeft {
			return tileView{}, false
		}
		return tileView{
			payload: render.PrintPayload(v, card, in.printIcons, usageAppLifetime),
			frame:   func() render.Frame { return render.PrintTileFrame(v, card) },
		}, true
	}
}

var printTile = tile{
	app:    "ember-print",
	card:   "print",
	toggle: func(in *tileInputs) bool { return in.printCfg.ClockAppEnabled() },
	live:   func(in *tileInputs) bool { return printInRotation(in.print) },
	view:   printTileOf(render.PrintCardTime, false),
}

var printReadyTile = tile{
	app:    "ember-print-eta",
	card:   "print-eta",
	toggle: func(in *tileInputs) bool { return in.printCfg.ClockAppEnabled() },
	live:   func(in *tileInputs) bool { return printInRotation(in.print) && printCounting(in.print) },
	view:   printTileOf(render.PrintCardReady, true),
}

var printPctTile = tile{
	app:    "ember-print-pct",
	card:   "print-pct",
	toggle: func(in *tileInputs) bool { return in.printCfg.ClockAppEnabled() },
	live:   func(in *tileInputs) bool { return printInRotation(in.print) && printCounting(in.print) },
	view:   printTileOf(render.PrintCardPct, true),
}

func samplePrintSnapshot(now time.Time) printer.Snapshot {
	return printer.Snapshot{
		Enabled:      true,
		Phase:        printer.PhasePrinting,
		Progress:     42,
		EndsAt:       now.Add(102 * time.Minute),
		Nozzle:       240,
		NozzleTarget: 240,
		Bed:          85,
		BedTarget:    85,
	}
}

type printNotifyState struct {
	lastID   uint64
	attnOn   bool
	attnNext time.Time
	retryAt  time.Time
}

func (c *coordinator) printTileInputs(in *tileInputs, now time.Time) {
	in.printCfg = c.loadCfg().Printer
	if c.printSnap != nil {
		in.print = c.printSnap(now)
	}
	in.printIcons = c.printIcons != nil && c.printIcons()
}

func (c *coordinator) fireNotice(kind noticeKind, payload map[string]any, rtttl string) error {
	n := notice{app: "print", kind: kind, priority: noticeQueue, payload: payload}
	if rtttl != "" {
		n.sound = noticeSound{rtttl: rtttl}
	}
	ctx, cancel := context.WithTimeout(c.runCtx(), printNoticeCallBudget)
	defer cancel()
	return c.showNotice(ctx, n)
}

func (c *coordinator) onPrint() {
	if c.snapshot == nil {
		return
	}
	now := c.clk.Now()
	c.reconcileTiles(now)
	c.checkPrint(now)
}

func (c *coordinator) checkPrint(now time.Time) {
	if c.printSnap == nil {
		return
	}
	cfg := c.loadCfg().Printer
	snap := c.printSnap(now)
	st := &c.printNotes
	if now.Before(st.retryAt) {
		return
	}
	for _, e := range snap.Events {
		if e.ID <= st.lastID {
			continue
		}
		if !c.printEvent(now, cfg, snap, e) {
			st.retryAt = now.Add(printRetryAfter)
			return
		}
		st.lastID = e.ID
	}
	if !st.attnOn || snap.Phase == printer.PhaseOffline {
		return
	}
	switch {
	case snap.Phase != printer.PhaseAttention || !snap.Enabled:
		ctx, cancel := context.WithTimeout(c.runCtx(), printNoticeCallBudget)
		defer cancel()
		if err := c.dismissNotice(ctx, noticePrintAttention); err != nil && !isAPINotFound(err) {
			c.logger.Warn("print attention dismiss failed", "err", err)
			st.retryAt = now.Add(printRetryAfter)
			return
		}
		st.attnOn = false
	case cfg.NotifyAttentionEnabled() && !now.Before(st.attnNext):
		if err := c.fireNotice(noticePrintAttention, render.PrintAttentionPopupPayload(printAttentionPopSec), printAttentionRTTTL); err != nil {
			c.logger.Warn("print attention popup failed", "err", err)
			st.retryAt = now.Add(printRetryAfter)
			return
		}
		st.attnNext = now.Add(printAttentionRepeat)
	}
}

func (c *coordinator) printEvent(now time.Time, cfg printer.Config, snap printer.Snapshot, e printer.Event) bool {
	st := &c.printNotes
	age := now.Sub(e.At)
	fail := func(what string, err error) bool {
		c.logger.Warn("print popup failed", "event", what, "err", err)
		return false
	}
	switch e.Kind {
	case printer.EventFinished:
		if !cfg.NotifyDoneEnabled() || age > printDoneGrace {
			return true
		}
		total := ""
		if !snap.StartedAt.IsZero() && snap.JobID == e.JobID {
			if d := e.At.Sub(snap.StartedAt); d > 0 {
				total = render.PrintDuration(int(d.Seconds()))
			}
		}
		if err := c.fireNotice(noticePrintDone, render.PrintDonePopupPayload(total, printDonePopupSec), printDoneRTTTL); err != nil {
			return fail("finished", err)
		}
	case printer.EventAttention:
		if !cfg.NotifyAttentionEnabled() || snap.Phase != printer.PhaseAttention {
			return true
		}
		if err := c.fireNotice(noticePrintAttention, render.PrintAttentionPopupPayload(printAttentionPopSec), printAttentionRTTTL); err != nil {
			return fail("attention", err)
		}
		st.attnOn, st.attnNext = true, now.Add(printAttentionRepeat)
	case printer.EventError:
		if !cfg.NotifyAttentionEnabled() || age > printDoneGrace {
			return true
		}
		if err := c.fireNotice(noticePrintError, render.PrintErrorPopupPayload(printErrorPopupSec), printErrorRTTTL); err != nil {
			return fail("error", err)
		}
	}
	return true
}
