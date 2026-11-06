package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/tarakanof/ember/internal/workday"
)

func (a *App) writeDayState(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errDayDisabled):
		writeError(w, http.StatusNotFound, err)
		return
	case errors.Is(err, errDayOff):
		writeError(w, http.StatusConflict, err)
		return
	case errors.Is(err, errDayStopAt), errors.Is(err, errDayAt):
		writeError(w, http.StatusBadRequest, err)
		return
	case err != nil:
		a.logger.Warn("day clock write failed", "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("day clock write failed"))
		return
	}
	k := a.dayBlock(a.dayNow(), a.moodState().Render.Running)
	if k == nil {
		writeError(w, http.StatusNotFound, errDayDisabled)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (a *App) handleDayState(w http.ResponseWriter, r *http.Request) {
	a.writeDayState(w, nil)
}

func (a *App) handleDayStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StopAt string `json:"stop_at"`
		At     string `json:"at"`
	}
	if !a.decodeOptionalOrReject(w, r, &req, true) {
		return
	}
	a.writeDayState(w, a.dayStart(a.dayNow(), req.StopAt, req.At))
}

func (a *App) handleDayBreak(w http.ResponseWriter, r *http.Request) {
	a.writeDayState(w, a.dayBreak(a.dayNow()))
}

func (a *App) handleDayResume(w http.ResponseWriter, r *http.Request) {
	a.writeDayState(w, a.dayResume(a.dayNow()))
}

func (a *App) handleDayStop(w http.ResponseWriter, r *http.Request) {
	a.writeDayState(w, a.dayStop(a.dayNow()))
}

func (a *App) handleDayStopAt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StopAt string `json:"stop_at"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	a.writeDayState(w, a.dayPutStopAt(a.dayNow(), req.StopAt))
}

func (a *App) handleDayOfferDismiss(w http.ResponseWriter, r *http.Request) {
	a.writeDayState(w, a.dayDismissOffer())
}

type dayBreakOut struct {
	Start time.Time  `json:"start"`
	End   *time.Time `json:"end"`
}

type dayHistoryOut struct {
	Date       string        `json:"date"`
	Start      *time.Time    `json:"start"`
	Stop       *time.Time    `json:"stop"`
	StopTarget *string       `json:"stop_target"`
	OnSec      int           `json:"on_sec"`
	BreakSec   int           `json:"break_sec"`
	Auto       bool          `json:"auto"`
	InRange    bool          `json:"in_range"`
	RepliesOn  int           `json:"replies_on"`
	RepliesOff int           `json:"replies_off"`
	FirstReply *time.Time    `json:"first_reply"`
	LastReply  *time.Time    `json:"last_reply"`
	Breaks     []dayBreakOut `json:"breaks"`
}

type dayWeekOut struct {
	Week        string `json:"week"`
	DaysWorked  int    `json:"days_worked"`
	DaysInRange int    `json:"days_in_range"`
	OnSec       int    `json:"on_sec"`
	RepliesOff  int    `json:"replies_off"`
}

func (a *App) dayHistory(now time.Time, n int) ([]workday.Day, error) {
	cal := a.dayCalendar()
	today := cal.DayStart(now)
	from := time.Date(today.Year(), today.Month(), today.Day()-(n-1), cal.DayStartHour, 0, 0, 0, cal.Loc)
	hi := now.Add(time.Second)
	events, err := a.store.DayEventsBetween(from.AddDate(0, 0, -1), hi)
	if err != nil {
		return nil, err
	}
	replies, err := a.store.DayRepliesBetween(from, hi)
	if err != nil {
		return nil, err
	}
	return cal.Days(workday.Replay(events), replies, now, n, a.cfg.Load().Day.grace()), nil
}

func (a *App) handleDayHistory(w http.ResponseWriter, r *http.Request) {
	if !a.dayOn() {
		writeError(w, http.StatusNotFound, errDayDisabled)
		return
	}
	days, err := a.dayHistory(a.dayNow(), clampQueryInt(r, "days", 14, 1, 120))
	if err != nil {
		a.logger.Warn("day history read failed", "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("day history read failed"))
		return
	}
	loc := a.statsLoc()
	out := make([]dayHistoryOut, 0, len(days))
	for _, d := range days {
		h := dayHistoryOut{
			Date: d.Date, Start: wireTimePtr(d.Start, loc), Stop: wireTimePtr(d.Stop, loc),
			OnSec: d.OnSec, BreakSec: d.BreakSec, Auto: d.Auto, InRange: d.InRange,
			RepliesOn: d.RepliesOn, RepliesOff: d.RepliesOff,
			FirstReply: wireTimePtr(d.FirstReply, loc), LastReply: wireTimePtr(d.LastReply, loc),
			Breaks: make([]dayBreakOut, 0, len(d.Breaks)),
		}
		if d.Worked() {
			target := d.StopTarget
			h.StopTarget = &target
		}
		for _, b := range d.Breaks {
			h.Breaks = append(h.Breaks, dayBreakOut{Start: wireTime(b.Start, loc), End: wireTimePtr(b.End, loc)})
		}
		out = append(out, h)
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": out})
}

func (a *App) handleDayWeeks(w http.ResponseWriter, r *http.Request) {
	if !a.dayOn() {
		writeError(w, http.StatusNotFound, errDayDisabled)
		return
	}
	now := a.dayNow()
	cal := a.dayCalendar()
	weeks := clampQueryInt(r, "weeks", 8, 1, 52)
	ws := cal.WeekStart(now)
	from := time.Date(ws.Year(), ws.Month(), ws.Day()-7*(weeks-1), cal.DayStartHour, 0, 0, 0, cal.Loc)
	days, err := a.dayHistory(now, cal.DaysBetween(from, now)+1)
	if err != nil {
		a.logger.Warn("day weeks read failed", "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("day weeks read failed"))
		return
	}
	out := make([]dayWeekOut, 0, weeks)
	for _, wk := range workday.Weeks(days) {
		out = append(out, dayWeekOut{Week: wk.Week, DaysWorked: wk.DaysWorked, DaysInRange: wk.DaysInRange,
			OnSec: wk.OnSec, RepliesOff: wk.RepliesOff})
	}
	writeJSON(w, http.StatusOK, map[string]any{"weeks": out})
}
