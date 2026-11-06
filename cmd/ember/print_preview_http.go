package main

import (
	"net/http"
	"time"

	"github.com/tarakanof/ember/internal/printer"
	"github.com/tarakanof/ember/internal/render"
)

func (a *App) handlePrintPreview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.printPreview(time.Now()))
}

func (a *App) printPreview(now time.Time) render.Preview {
	return printPreviewOf(a.cfg.Load().Printer, a.printer.State.At(now), now)
}

func printPreviewOf(cfg printer.Config, snap printer.Snapshot, now time.Time) render.Preview {
	in := tileInputs{now: now, printCfg: cfg, print: snap}
	if !printInRotation(in.print, in.printCfg) {
		in.print = samplePrintSnapshot(now)
	}
	var cards []string
	for _, t := range printTiles {
		if t.live(&in) {
			cards = append(cards, t.card)
		}
	}
	return previewTiles(in, cards...)
}
