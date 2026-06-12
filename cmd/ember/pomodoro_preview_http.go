package main

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/tarakanof/ember/internal/render"
)

// handlePomodoroPreview renders one drawn frame per Pomodoro phase under a
// draft config, in the same 32×8 grid as /v1/preview. Open and read-only,
// like the weather preview. The clock itself shows the native animated icon +
// firmware text (PomodoroPayload); the preview shows the drawn equivalent
// (RenderPomodoro) — same colours, countdown and progress bar.
//
// Query params:
//   - focus_minutes        int 1..480 (default 25)
//   - short_break_minutes  int 1..60  (default 5)
//   - long_break_minutes   int 1..180 (default 15)
//   - focus_color          "#RRGGBB" (default: built-in phase colour)
//   - break_color          "#RRGGBB" (default: built-in phase colour)
//
// Each countdown shows 70% of the phase remaining, so the progress bar reads
// as mid-session rather than full or empty.
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

// queryIntClamped reads an integer query param, falling back to def when
// absent or malformed and clamping into [min, max].
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
