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

// hourly returns n rising sample values, usable as both °C and AQI.
func hourly(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 10 + float64(i)
	}
	return out
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
	pomo := PomodoroView{Phase: "focus", RemainingSec: 1050, PlannedSec: 1500}

	frame := func(f Frame) [panelW]bool { return row7FromFrame(&f) }
	cases := map[string][panelW]bool{
		"agent session bar":         frame(ComposeFrame(running, cardSource, nil, sessions, now)),
		"agent rate bar":            frame(ComposeFrame(rateMode, cardSource, nil, sessions, now)),
		"agent usage face":          frame(ComposeFrame(running, cardUsage7d, u, sessions, now)),
		"agent idle usage 5h":       row7FromPayload(t, RenderIdleUsagePayload(map[string]*UsageView{"claude": u}, 0, now, 60)),
		"agent idle usage 7d":       row7FromPayload(t, RenderIdleUsagePayload(map[string]*UsageView{"claude": u}, 1, now, 60)),
		"agent tool card":           row7FromPayload(t, RenderForCoord(Snapshot{Now: now, Sessions: sessions}, running.Key(), 1, false, 60, nil)),
		"agent attention":           row7FromPayload(t, RenderForCoord(Snapshot{Now: now, Sessions: sessions}, waiting.Key(), 0, true, 60, nil)),
		"agent tool card, rate bar": row7FromPayload(t, RenderForCoord(Snapshot{Now: now, Sessions: []Session{rateMode}}, rateMode.Key(), 1, false, 60, nil)),
		"weather tile 24h":          row7FromPayload(t, WeatherPayload(WeatherClouds, "21°", hourly(24), 60)),
		"weather tile 6h":           row7FromPayload(t, WeatherPayload(WeatherSnow, "-12°", hourly(6), 60)),
		"weather tile native icon":  row7FromPayload(t, WeatherPayloadNative("2286", "21°", hourly(24), 60)),
		"weather tile moon":         row7FromPayload(t, WeatherPayloadMoon("9°", hourly(24), MoonView{Illum: 0.6, Waxing: true}, 60)),
		"air tile":                  row7FromPayload(t, AirPayload(53, hourly(24), 60)),
		"pomodoro device":           row7FromPayload(t, PomodoroPayload(pomo, 60)),
		"pomodoro preview":          row7FromFrame(RenderPomodoro(pomo)),
	}
	for name, row := range cases {
		if got := firstBarCol(row); got != barX0 {
			t.Errorf("%s: row-7 bar starts at col %d, want barX0=%d", name, got, barX0)
		}
	}
}

// TestTextPayloadsMaskTheIconGap pins the drawn-icon op of every payload that
// carries native text to cols 0-8 with col 8 blank. Without a native icon NG
// scrolls text across the whole panel and paints draw ops over it, so an 8-wide
// op leaves the gap column exposed and glyphs touch the icon mid-scroll.
func TestTextPayloadsMaskTheIconGap(t *testing.T) {
	now := time.Unix(1_790_380_000, 0)
	s := Session{Source: "m4", Tool: "claude", Session: "a", State: "running", Activity: "Bash: go test ./..."}
	w := s
	w.State = "waiting"
	cases := map[string]map[string]any{
		"agent tool card": RenderForCoord(Snapshot{Now: now, Sessions: []Session{s}}, s.Key(), 1, false, 60, nil),
		"agent attention": RenderForCoord(Snapshot{Now: now, Sessions: []Session{w}}, w.Key(), 0, true, 60, nil),
		"weather popup":   WeatherPopupPayload(WeatherRain, "RAIN 12°", "", 5),
		"air popup":       AirPopupPayload(85, 5),
		"sun popup":       SunPopupPayload(true, "SUNRISE 6:42", 5),
		"meeting tile":    MeetingPayload("STANDUP", 12, 60),
		"meeting popup":   MeetingPopupPayload("STANDUP", 2, 5),
		"reminder popup":  ReminderPopupPayload("Call mom", "", 5, false),
		"limit reset":     LimitResetPopupPayload("claude", 5),
	}
	for name, p := range cases {
		if _, ok := p["text"]; !ok {
			t.Fatalf("%s: no native text; case does not belong here", name)
		}
		var icon []any
		for _, o := range p["draw"].([]any) {
			if op := o.([]any); op[1] == 0 && op[2] == 0 {
				icon = op
			}
		}
		if icon == nil {
			t.Fatalf("%s: no icon op at (0,0)", name)
		}
		if icon[3] != iconOpW || icon[4] != 8 {
			t.Errorf("%s: icon op is %vx%v, want %dx8", name, icon[3], icon[4], iconOpW)
			continue
		}
		data := icon[5].([]int)
		for y := 0; y < 8; y++ {
			if v := data[y*iconOpW+iconW]; v != 0 {
				t.Errorf("%s: gap col %d row %d = %#06x, want 0", name, iconW, y, v)
			}
		}
	}
}

// TestHourlyStripsFillTheBar pins the weather and air strips to the full
// bottom bar: a 24 h window is one column per hour (none dropped), and a
// shorter window is stretched so the strip never ends in a ragged dark tail.
func TestHourlyStripsFillTheBar(t *testing.T) {
	for _, n := range []int{6, 12, 22, 24} {
		temps := hourly(n)
		f := WeatherTileFrame(WeatherClouds, "21°", temps, nil)
		for x := barX0; x < panelW; x++ {
			if !f.Dirty[barRow][x] {
				t.Errorf("weather %dh: col %d of the strip is dark", n, x)
			}
		}
		if got, want := f.Pixels[barRow][panelW-1], TempColor(temps[n-1]); got != want {
			t.Errorf("weather %dh: last col = %v, want the last hour %v", n, got, want)
		}
		if got, want := f.Pixels[barRow][barX0], TempColor(temps[0]); got != want {
			t.Errorf("weather %dh: first col = %v, want the first hour %v", n, got, want)
		}
		a := AirTileFrame(53, temps)
		if got, want := a.Pixels[barRow][panelW-1], AQIColor(temps[n-1]); got != want {
			t.Errorf("air %dh: last col = %v, want the last hour %v", n, got, want)
		}
		for x := barX0; x < panelW; x++ {
			if !a.Dirty[barRow][x] {
				t.Errorf("air %dh: col %d of the strip is dark", n, x)
			}
		}
	}
}

// TestTileDigitsSitOnTheTextRow pins the weather and air readouts to rows
// 1-5, the rows every other app (agent digits, NG's native text) uses, so the
// digits do not jump a row as the rotation moves between apps. Row 6 stays a
// blank spacer above the bottom bar.
func TestTileDigitsSitOnTheTextRow(t *testing.T) {
	frames := map[string]Frame{
		"weather": WeatherTileFrame(WeatherClouds, "21°", hourly(24), nil),
		"air":     AirTileFrame(53, hourly(24)),
	}
	for name, f := range frames {
		lit := false
		for x := contentX; x < panelW; x++ {
			if f.Dirty[0][x] {
				t.Errorf("%s: row 0 col %d lit, want digits on rows 1-5", name, x)
			}
			if f.Dirty[6][x] {
				t.Errorf("%s: row 6 col %d lit, want a blank spacer above the bar", name, x)
			}
			lit = lit || f.Dirty[textRow][x]
		}
		if !lit {
			t.Errorf("%s: nothing on row %d", name, textRow)
		}
	}
}
