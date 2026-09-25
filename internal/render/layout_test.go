package render

import (
	"testing"
	"time"
)

// row7FromFrame returns which cols of row 7 a drawn frame lights.
func row7FromFrame(f *Frame) [panelW]bool {
	var row [panelW]bool
	for x := 0; x < panelW; x++ {
		row[x] = f.Dirty[barRow][x] && f.Pixels[barRow][x] != (RGB{})
	}
	return row
}

// row7FromPayload composites row 7 of a pushed-app payload the way NG does:
// the native progress bar (from x=8 with an icon, else x=0), then draw ops on
// top, zeros included.
func row7FromPayload(t *testing.T, p map[string]any) [panelW]bool {
	t.Helper()
	var row [panelW]bool
	if pr, ok := p["progress"].(int); ok && pr >= 0 {
		x0 := 0
		if _, icon := p["icon"]; icon {
			x0 = iconW
		}
		for x := x0; x < panelW; x++ {
			row[x] = true // track or fill: both light the row
		}
	}
	ops, _ := p["draw"].([]any)
	for _, o := range ops {
		op := o.([]any)
		x0, y0, w, h := op[1].(int), op[2].(int), op[3].(int), op[4].(int)
		data := op[5].([]int)
		if barRow < y0 || barRow >= y0+h {
			continue
		}
		for i := 0; i < w; i++ {
			if x := x0 + i; x >= 0 && x < panelW {
				row[x] = data[(barRow-y0)*w+i] != 0
			}
		}
	}
	return row
}

// firstBarCol is the leftmost lit row-7 column right of the icon sprite, or -1.
// Cols 0-7 are skipped: some icons (the sun, the calendar) reach row 7.
func firstBarCol(row [panelW]bool) int {
	for x := iconW; x < panelW; x++ {
		if row[x] {
			return x
		}
	}
	return -1
}

// TestEveryBottomBarStartsAtBarX0 pins the shared grid: whatever an app puts
// on row 7 (session bar, rate bar, usage bar, forecast/AQI strip, Pomodoro
// progress) starts in the same column, right after the icon, so the bar does
// not jump sideways as the device rotates between apps.
func TestEveryBottomBarStartsAtBarX0(t *testing.T) {
	now := time.Unix(1_790_380_000, 0)
	ctx, rate := 47, 72
	running := Session{Source: "m4", Tool: "claude", Session: "a", State: "running", ContextPct: &ctx, RateWindowPct: &rate, Activity: "Bash: go test"}
	waiting := running
	waiting.Session, waiting.State = "b", "waiting"
	sessions := []Session{running, waiting}
	rateMode := running
	rateMode.RateBottomBar = true
	seven := 42
	u := &UsageView{FiveHourPct: 87, ResetLabel: "17:30", SevenDayPct: &seven}

	frame := func(f Frame) [panelW]bool { return row7FromFrame(&f) }
	cases := map[string][panelW]bool{
		"agent session bar":   frame(ComposeFrame(running, cardSource, nil, sessions, now)),
		"agent rate bar":      frame(ComposeFrame(rateMode, cardSource, nil, sessions, now)),
		"agent usage face":    frame(ComposeFrame(running, cardUsage7d, u, sessions, now)),
		"agent idle usage 5h": row7FromPayload(t, RenderIdleUsagePayload(map[string]*UsageView{"claude": u}, 0, now, 60)),
		"agent idle usage 7d": row7FromPayload(t, RenderIdleUsagePayload(map[string]*UsageView{"claude": u}, 1, now, 60)),
	}
	for name, row := range cases {
		if got := firstBarCol(row); got != barX0 {
			t.Errorf("%s: row-7 bar starts at col %d, want barX0=%d", name, got, barX0)
		}
	}
}
