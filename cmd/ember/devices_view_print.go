package main

import (
	"time"

	"github.com/tarakanof/ember/internal/devices"
	"github.com/tarakanof/ember/internal/printer"
)

const (
	knobPrintLabelRunes    = 32
	knobPrintMaterialRunes = 16
)

type knobPrint struct {
	Phase        printer.Phase   `json:"phase"`
	Progress     *int            `json:"progress,omitempty"`
	EndsAt       *int64          `json:"ends_at,omitempty"`
	StartedAt    *int64          `json:"started_at,omitempty"`
	ChangeAt     *int64          `json:"change_at,omitempty"`
	Nozzle       *int            `json:"nozzle,omitempty"`
	NozzleTarget *int            `json:"nozzle_target,omitempty"`
	Bed          *int            `json:"bed,omitempty"`
	BedTarget    *int            `json:"bed_target,omitempty"`
	Name         string          `json:"name,omitempty"`
	Material     string          `json:"material,omitempty"`
	ThumbVersion string          `json:"thumb_version,omitempty"`
	Event        *knobPrintEvent `json:"event,omitempty"`
	Printer      string          `json:"printer"`
}

type knobPrintEvent struct {
	ID   uint64            `json:"id"`
	Kind printer.EventKind `json:"kind"`
	At   int64             `json:"at"`
}

func knobWantsPrint(caps *devices.Caps, cfg devices.KnobSettings) bool {
	if caps == nil {
		return false
	}
	page := caps.HasPage(devices.KnobPrintPage) && cfg.PageOn(devices.KnobPrintPage)
	moments := caps.HasFeature(featurePrintMoments) && caps.HasPage("bot") && cfg.PageOn("bot") && cfg.PrintMomentsOn()
	return page || moments
}

func printThumbVersion(ver string) string {
	if ver == "" {
		return ""
	}
	return ver + "-" + printer.ThumbRecipe
}

func (a *App) knobPrint(now time.Time) *knobPrint {
	return newKnobPrint(a.printer.State.At(now))
}

func newKnobPrint(s printer.Snapshot) *knobPrint {
	if !s.Enabled || s.Phase == printer.PhaseOff {
		return nil
	}
	p := &knobPrint{Phase: s.Phase, Printer: firstRunes(s.Printer.Name, knobPrintLabelRunes)}
	if e := s.Event; e != nil {
		p.Event = &knobPrintEvent{ID: e.ID, Kind: e.Kind, At: e.At.Unix()}
	}
	switch s.Phase {
	case printer.PhaseOffline, printer.PhaseIdle:
		return p
	}
	p.Progress = new(s.Progress)
	p.EndsAt, p.StartedAt, p.ChangeAt = unixOrNil(s.EndsAt), unixOrNil(s.StartedAt), unixOrNil(s.ChangeAt)
	if s.Phase == printer.PhaseHeating {
		p.Nozzle, p.NozzleTarget = degrees(s.Nozzle), degrees(s.NozzleTarget)
		p.Bed, p.BedTarget = degrees(s.Bed), degrees(s.BedTarget)
	}
	p.Name = s.Name
	p.Material = firstRunes(s.Material, knobPrintMaterialRunes)
	p.ThumbVersion = printThumbVersion(s.ThumbVersion)
	return p
}

func firstRunes(s string, n int) string {
	i := 0
	for at := range s {
		if i == n {
			return s[:at]
		}
		i++
	}
	return s
}
