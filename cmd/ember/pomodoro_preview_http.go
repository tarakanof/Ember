package main

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/tarakanof/ember/internal/render"
)

func (a *App) handlePomodoroPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	focusMin := queryIntClamped(q, "focus_minutes", 25, 1, 480)
	shortMin := queryIntClamped(q, "short_break_minutes", 5, 1, 60)
	longMin := queryIntClamped(q, "long_break_minutes", 15, 1, 180)
	focusColor, _ := render.HexRGB(q.Get("focus_color"))
	breakColor, _ := render.HexRGB(q.Get("break_color"))

	p := render.Preview{Width: 32, Height: 8, Frames: []render.CardFrame{}}
	for _, ph := range []struct {
		card    string
		minutes int
	}{
		{"focus", focusMin},
		{"short_break", shortMin},
		{"long_break", longMin},
	} {
		planned := ph.minutes * 60
		f := render.RenderPomodoro(render.PomodoroView{
			Phase:        ph.card,
			RemainingSec: planned * 7 / 10,
			PlannedSec:   planned,
			FocusColor:   focusColor,
			BreakColor:   breakColor,
		})
		p.Frames = append(p.Frames, render.CardFrame{Card: ph.card, Pixels: render.HexPixels(f)})
	}
	writeJSON(w, http.StatusOK, p)
}

func queryIntClamped(q url.Values, key string, def, min, max int) int {
	v, err := strconv.Atoi(q.Get(key))
	if err != nil {
		v = def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
