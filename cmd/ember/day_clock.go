package main

import (
	"errors"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/workday"
)

var (
	errDayDisabled = errors.New("day clock is not enabled")
	errDayOff      = errors.New("day clock is off")
	errDayStopAt   = errors.New("stop_at must be HH:MM")
	errDayAt       = errors.New("at must be HH:MM and not before the last day event")
)

const (
	dayOfferFor      = time.Hour
	daySummaryFor    = 5 * time.Second
	dayReplyThrottle = time.Minute
	dayRetention     = 400 * 24 * time.Hour
	daySweepInterval = time.Hour
	dayTickInterval  = 15 * time.Second
	dayEndedMemory   = 24 * time.Hour
)

type dayTracker struct {
	mu         sync.Mutex
	open       *workday.Period
	lastStopAt string
	lastEvent  time.Time
	lastStart  time.Time
	lastOut    time.Time
	armed      bool
	lastReply  time.Time
	offReplies int
	replyMarks map[string]time.Time
	ended      map[string]dayEnded
	offerAt    time.Time
	offerDay   string
	summary    *knobDaySummary
	summaryEnd time.Time
	sweptAt    time.Time
}

type dayEnded struct {
	state string
	at    time.Time
}

type knobDay struct {
	State               string          `json:"state"`
	Started             string          `json:"started,omitempty"`
	StopAt              string          `json:"stop_at"`
	OnMin               int             `json:"on_min"`
	BreakMin            int             `json:"break_min"`
	OverMin             int             `json:"over_min"`
	OfferStart          string          `json:"offer_start,omitempty"`
	RepliesOffLastNight int             `json:"replies_off_last_night,omitempty"`
	Agents              int             `json:"agents"`
	BreakHint           bool            `json:"break_hint,omitempty"`
	Summary             *knobDaySummary `json:"summary,omitempty"`
}

type knobDaySummary struct {
	OnMin       int  `json:"on_min"`
	InRange     bool `json:"in_range"`
	WeekInRange int  `json:"week_in_range"`
	WeekDays    int  `json:"week_days"`
}

func (a *App) dayOn() bool { return a.store != nil && a.cfg.Load().Day.IsEnabled() }

func (a *App) dayCalendar() workday.Calendar {
	return workday.Calendar{DayStartHour: a.cfg.Load().Pomodoro.DayStartHour, Loc: a.statsLoc()}
}

func (a *App) dayNow() time.Time {
	if a.dayClock != nil {
		return a.dayClock()
	}
	return time.Now()
}

func (a *App) dayChanged() {
	a.changes.notify(topicDay)
	if a.coord != nil {
		a.coord.Send(coordCmd{kind: cmdTick})
	}
}

func (a *App) loadDay() error {
	if a.store == nil {
		return nil
	}
	events, armed, err := a.store.DayEventsFromLastStart()
	if err != nil {
		return err
	}
	lastReply, _, err := a.store.LastDayReply()
	if err != nil {
		return err
	}
	log := workday.Replay(events)
	d := &a.day
	d.mu.Lock()
	defer d.mu.Unlock()
	d.open = nil
	if cur := log.Current(); cur != nil {
		p := *cur
		d.open = &p
	}
	if n := len(log.Periods); n > 0 {
		d.lastStart = log.Periods[n-1].Start
	}
	if n := len(events); n > 0 {
		d.lastEvent = events[n-1].At
	}
	d.lastStopAt = log.LastStopAt
	d.lastOut = log.LastClockOut()
	d.armed = armed
	d.lastReply = lastReply
	d.offReplies = 0
	if d.open == nil && !d.lastOut.IsZero() {
		if d.offReplies, err = a.store.CountDayReplies(d.lastOut.Add(time.Second)); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) dayRecord(d *dayTracker, e workday.Event) (time.Time, error) {
	if e.At.Before(d.lastEvent) {
		e.At = d.lastEvent
	}
	if err := a.store.RecordDayEvent(e); err != nil {
		return time.Time{}, err
	}
	d.lastEvent = e.At
	return e.At, nil
}

func (a *App) dayAction(fn func(d *dayTracker) (bool, error)) error {
	if !a.dayOn() {
		return errDayDisabled
	}
	d := &a.day
	d.mu.Lock()
	changed, err := fn(d)
	d.mu.Unlock()
	if changed {
		a.dayChanged()
	}
	return err
}

func (a *App) dayStart(now time.Time, stopAt, at string) error {
	cal := a.dayCalendar()
	def := a.cfg.Load().Day.defaultStop()
	return a.dayAction(func(d *dayTracker) (bool, error) {
		if d.open != nil {
			return false, nil
		}
		if stopAt == "" {
			stopAt = d.stopTarget(def)
		}
		if _, _, ok := workday.ParseHHMM(stopAt); !ok {
			return false, errDayStopAt
		}
		start := now
		if at != "" {
			var ok bool
			if start, ok = cal.ResolveAt(at, now); !ok || start.Before(d.lastEvent) {
				return false, errDayAt
			}
		}
		start, err := a.dayRecord(d, workday.Event{At: start, Kind: workday.Start, Value: stopAt})
		if err != nil {
			return false, err
		}
		d.open = &workday.Period{Start: start, StopAt: stopAt}
		d.lastStart, d.lastStopAt, d.armed = start, stopAt, true
		d.offerAt, d.offReplies, d.summary = time.Time{}, 0, nil
		return true, nil
	})
}

func (a *App) dayBreak(now time.Time) error {
	return a.dayAction(func(d *dayTracker) (bool, error) {
		if d.open == nil {
			return false, errDayOff
		}
		if d.open.OnBreak() {
			return false, nil
		}
		at, err := a.dayRecord(d, workday.Event{At: now, Kind: workday.Break})
		if err != nil {
			return false, err
		}
		d.open.Breaks = append(d.open.Breaks, workday.Interval{Start: at})
		return true, nil
	})
}

func (a *App) dayResume(now time.Time) error {
	return a.dayAction(func(d *dayTracker) (bool, error) {
		if d.open == nil {
			return false, errDayOff
		}
		if !d.open.OnBreak() {
			return false, nil
		}
		at, err := a.dayRecord(d, workday.Event{At: now, Kind: workday.Resume})
		if err != nil {
			return false, err
		}
		d.open.Breaks[len(d.open.Breaks)-1].End = at
		return true, nil
	})
}

func (a *App) dayStop(now time.Time) error {
	cal := a.dayCalendar()
	grace := a.cfg.Load().Day.grace()
	return a.dayAction(func(d *dayTracker) (bool, error) {
		if d.open == nil {
			return false, nil
		}
		p, err := a.dayClose(d, now, false)
		if err != nil {
			return false, err
		}
		s := knobDaySummary{OnMin: p.OnSec(p.End) / 60, InRange: cal.InRange(p, grace)}
		if s.WeekDays, s.WeekInRange, err = a.dayWeekSoFar(cal, now, grace); err != nil {
			a.logger.Warn("day summary week read failed", "err", err)
		}
		d.summary, d.summaryEnd = &s, now.Add(daySummaryFor)
		return true, nil
	})
}

func (a *App) dayClose(d *dayTracker, at time.Time, auto bool) (workday.Period, error) {
	kind := workday.Stop
	if auto {
		kind = workday.AutoStop
	}
	at, err := a.dayRecord(d, workday.Event{At: at, Kind: kind})
	if err != nil {
		return workday.Period{}, err
	}
	p := *d.open
	p.Close(at, auto)
	d.open, d.lastOut, d.offReplies = nil, at, 0
	a.logger.Info("day clocked out", "auto", auto, "on_min", p.OnSec(at)/60, "stop_at", p.StopAt)
	return p, nil
}

func (a *App) dayWeekSoFar(cal workday.Calendar, now time.Time, grace time.Duration) (days, inRange int, err error) {
	from := cal.WeekStart(now)
	events, err := a.store.DayEventsBetween(from, now.Add(time.Second))
	if err != nil {
		return 0, 0, err
	}
	n := cal.DaysBetween(from, now) + 1
	for _, w := range workday.Weeks(cal.Days(workday.Replay(events), nil, now, n, grace)) {
		days += w.DaysWorked
		inRange += w.DaysInRange
	}
	return days, inRange, nil
}

func (a *App) dayPutStopAt(now time.Time, stopAt string) error {
	return a.dayAction(func(d *dayTracker) (bool, error) {
		if _, _, ok := workday.ParseHHMM(stopAt); !ok {
			return false, errDayStopAt
		}
		if _, err := a.dayRecord(d, workday.Event{At: now, Kind: workday.StopAt, Value: stopAt}); err != nil {
			return false, err
		}
		if d.open != nil {
			d.open.StopAt = stopAt
		}
		d.lastStopAt = stopAt
		return true, nil
	})
}

func (a *App) dayDismissOffer() error {
	return a.dayAction(func(d *dayTracker) (bool, error) {
		was := !d.offerAt.IsZero()
		d.offerAt = time.Time{}
		return was, nil
	})
}

func (d *dayTracker) stopTarget(def string) string {
	if d.lastStopAt != "" {
		return d.lastStopAt
	}
	return def
}

func (a *App) noteDaySessionEnded(s Session) {
	if (s.State != "done" && s.State != "waiting") || !a.dayOn() {
		return
	}
	d := &a.day
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ended == nil {
		d.ended = map[string]dayEnded{}
	}
	d.ended[s.Key()] = dayEnded{state: s.State, at: s.UpdatedAt}
}

func (a *App) noteDayReply(s Session, prior string, now time.Time) {
	if !a.dayOn() {
		return
	}
	cal := a.dayCalendar()
	key := s.Key()
	d := &a.day
	d.mu.Lock()
	if e, ok := d.ended[key]; ok {
		delete(d.ended, key)
		if prior == "" && now.Sub(e.at) < dayEndedMemory {
			prior = e.state
		}
	}
	if s.State != "running" || (prior != "done" && prior != "waiting") {
		d.mu.Unlock()
		return
	}
	if last, ok := d.replyMarks[key]; ok && now.Sub(last) < dayReplyThrottle {
		d.mu.Unlock()
		return
	}
	if d.replyMarks == nil {
		d.replyMarks = map[string]time.Time{}
	}
	d.replyMarks[key] = now
	d.lastReply = now
	off := d.open == nil
	if off {
		d.offReplies++
		today := cal.DayKey(now)
		if d.offerDay != today && (d.lastStart.IsZero() || cal.DayKey(d.lastStart) != today) {
			d.offerAt, d.offerDay = now, today
		}
	}
	d.mu.Unlock()
	if err := a.store.RecordDayReply(workday.Reply{At: now, Source: s.Source, Tool: s.Tool, SessionKey: key}); err != nil {
		a.logger.Warn("day reply record failed", "err", err)
	}
	if off {
		a.dayChanged()
	}
}

func (a *App) dayTick(now time.Time) {
	if !a.dayOn() {
		return
	}
	cal := a.dayCalendar()
	auto := a.cfg.Load().Day.autoStop()
	d := &a.day
	d.mu.Lock()
	changed := false
	if at, ok := d.autoStopAt(cal, auto, now); ok {
		if _, err := a.dayClose(d, at, true); err != nil {
			a.logger.Warn("day auto clock-out failed", "err", err)
		} else {
			changed = true
		}
	}
	sweep := now.Sub(d.sweptAt) >= daySweepInterval
	if sweep {
		d.sweptAt = now
		for k, t := range d.replyMarks {
			if now.Sub(t) >= dayReplyThrottle {
				delete(d.replyMarks, k)
			}
		}
		for k, e := range d.ended {
			if now.Sub(e.at) >= dayEndedMemory {
				delete(d.ended, k)
			}
		}
	}
	d.mu.Unlock()
	if sweep {
		if err := a.store.PruneDay(now.Add(-dayRetention)); err != nil {
			a.logger.Warn("day prune failed", "err", err)
		}
	}
	if changed {
		a.dayChanged()
	}
}

func (d *dayTracker) autoStopAt(cal workday.Calendar, auto time.Duration, now time.Time) (time.Time, bool) {
	p := d.open
	if p == nil {
		return time.Time{}, false
	}
	stop, ok := cal.StopInstant(p.Start, p.StopAt)
	if !ok {
		return time.Time{}, false
	}
	base := later(stop, d.lastReply)
	due := !now.Before(stop) && now.Sub(base) >= auto
	if !due && now.Before(cal.DayEnd(p.Start)) {
		return time.Time{}, false
	}
	at := later(base, d.lastEvent)
	if at.After(now) {
		at = now
	}
	return at, true
}

func (a *App) dayQuiet() bool {
	if !a.dayOn() {
		return false
	}
	d := &a.day
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.armed && d.open == nil
}

func (a *App) quietAt(t time.Time) bool { return a.cfg.Load().quietAt(t) || a.dayQuiet() }

func (a *App) dayBlock(now time.Time, agents int) *knobDay {
	if !a.dayOn() {
		return nil
	}
	cfg := a.cfg.Load().Day
	cal := a.dayCalendar()
	hhmm := func(t time.Time) string { return t.In(cal.Loc).Format("15:04") }
	d := &a.day
	d.mu.Lock()
	defer d.mu.Unlock()
	k := &knobDay{State: "off", StopAt: d.stopTarget(cfg.defaultStop()), Agents: agents}
	if p := d.open; p != nil {
		k.State, k.Started, k.StopAt = "on", hhmm(p.Start), p.StopAt
		k.OnMin = p.OnSec(now) / 60
		k.BreakMin = p.BreakSec(now) / 60
		if p.OnBreak() {
			k.State, k.BreakMin = "break", p.CurrentBreakSec(now)/60
		}
		if stop, ok := cal.StopInstant(p.Start, p.StopAt); ok && now.After(stop) {
			k.OverMin = int(now.Sub(stop) / time.Minute)
		}
		hint := cfg.breakHint()
		k.BreakHint = hint > 0 && !p.OnBreak() && now.Sub(p.WorkingSince()) >= hint
	} else {
		if !d.offerAt.IsZero() && now.Sub(d.offerAt) < dayOfferFor {
			k.OfferStart = hhmm(d.offerAt)
		}
		if !d.lastOut.IsZero() && d.lastOut.Before(cal.DayStart(now)) {
			k.RepliesOffLastNight = d.offReplies
		}
	}
	if d.summary != nil && now.Before(d.summaryEnd) {
		s := *d.summary
		k.Summary = &s
	}
	return k
}
