package main

import (
	"net/http"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func (a *App) handlePrintPreview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.printPreview(time.Now()))
}

func (a *App) printPreview(now time.Time) render.Preview {
	in := tileInputs{now: now, printCfg: a.cfg.Load().Printer, print: a.printer.State.At(now)}
	if !printInRotation(in.print) {
		in.print = samplePrintSnapshot(now)
	}
	return previewTiles(in, printTile.card, printReadyTile.card, printPctTile.card)
}
