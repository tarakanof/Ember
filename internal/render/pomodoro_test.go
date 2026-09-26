package render

import "testing"

// countRowColor counts how many pixels on row y are lit with exactly color c.
func countRowColor(f *Frame, y int, c RGB) int {
	n := 0
	for x := 0; x < 32; x++ {
		if f.Dirty[y][x] && f.Pixels[y][x] == c {
			n++
		}
	}
	return n
}

func TestRenderPomodoroFocusUsesFocusColorForTime(t *testing.T) {
	fc := RGB{0xff, 0x3b, 0x30}
	f := RenderPomodoro(PomodoroView{
		Phase: "focus", RemainingSec: 25 * 60, PlannedSec: 25 * 60, FocusColor: fc,
	})
	// "25:00": first digit '2' top-left pixel sits at the time origin.
	if !f.Dirty[1][pomoTimeX] || f.Pixels[1][pomoTimeX] != fc {
		t.Fatalf("time digit at (%d,1) not painted in focus color; dirty=%v px=%+v", pomoTimeX, f.Dirty[1][pomoTimeX], f.Pixels[1][pomoTimeX])
	}
	// A pictogram occupies the left columns (0..7).
	lit := 0
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if f.Dirty[y][x] {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Fatal("expected a phase pictogram in cols 0..7, found none")
	}
}

func TestRenderPomodoroProgressBarShrinksWithRemaining(t *testing.T) {
	fc := RGB{0xff, 0x3b, 0x30}
	full := RenderPomodoro(PomodoroView{Phase: "focus", RemainingSec: 1500, PlannedSec: 1500, FocusColor: fc})
	if w := countRowColor(full, 7, fc); w != barW {
		t.Fatalf("full progress width = %d, want %d", w, barW)
	}
	half := RenderPomodoro(PomodoroView{Phase: "focus", RemainingSec: 750, PlannedSec: 1500, FocusColor: fc})
	if w := countRowColor(half, 7, fc); w != barW/2 {
		t.Fatalf("half progress width = %d, want %d", w, barW/2)
	}
	none := RenderPomodoro(PomodoroView{Phase: "focus", RemainingSec: 0, PlannedSec: 1500, FocusColor: fc})
	if w := countRowColor(none, 7, fc); w != 0 {
		t.Fatalf("empty progress width = %d, want 0", w)
	}
}

func TestRenderPomodoroBreakUsesBreakColor(t *testing.T) {
	bc := RGB{0x2e, 0xe8, 0x5e}
	f := RenderPomodoro(PomodoroView{Phase: "short_break", RemainingSec: 300, PlannedSec: 300, BreakColor: bc})
	if !f.Dirty[1][pomoTimeX] || f.Pixels[1][pomoTimeX] != bc {
		t.Fatalf("break time digit not painted in break color; px=%+v", f.Pixels[1][pomoTimeX])
	}
}

func TestRenderPomodoroBreakCupIsGrayNotBreakColor(t *testing.T) {
	bc := RGB{0x2e, 0xe8, 0x5e} // green
	for _, phase := range []string{"short_break", "long_break"} {
		f := RenderPomodoro(PomodoroView{Phase: phase, RemainingSec: 300, PlannedSec: 300, BreakColor: bc})
		assertGrayMugRim(t, phase, f, bc)
	}
}

// assertGrayMugRim checks the break pictogram is the coffee mug: the device
// shows the coffee icon for both breaks, so the preview must too.
func assertGrayMugRim(t *testing.T, phase string, f *Frame, bc RGB) {
	t.Helper()
	// The mug rim (row 2, cols 1..5) must be the neutral gray, never the break colour.
	cupPixels := 0
	for x := 1; x <= 5; x++ {
		if !f.Dirty[2][x] {
			t.Fatalf("%s: mug rim pixel (%d,2) not painted", phase, x)
		}
		if f.Pixels[2][x] == bc {
			t.Fatalf("%s: mug rim pixel (%d,2) painted in break colour; want gray", phase, x)
		}
		if f.Pixels[2][x] == pomoCupGray {
			cupPixels++
		}
	}
	if cupPixels != 5 {
		t.Fatalf("%s: gray mug rim pixels = %d, want 5", phase, cupPixels)
	}
}

// TestRenderPomodoroMatchesTheDeviceLayout pins the drawn preview to what the
// device shows for PomodoroPayload: NG centres the MM:SS in cols 9-31 after
// the native icon, and draws the native progress along row 7 from col 8, not
// under the icon.
func TestRenderPomodoroMatchesTheDeviceLayout(t *testing.T) {
	f := RenderPomodoro(PomodoroView{Phase: "focus", RemainingSec: 1500, PlannedSec: 1500})
	for x := 0; x < barX0; x++ {
		if f.Dirty[barRow][x] {
			t.Errorf("progress painted col %d under the icon", x)
		}
	}
	// "25:00" is 17 px wide (tight colon): centred in 23 cols leaves 3 each side.
	if want := contentX + (contentW-17)/2; pomoTimeX != want {
		t.Errorf("pomoTimeX = %d, want %d (centred)", pomoTimeX, want)
	}
	last := pomoTimeX + 16
	if !f.Dirty[1][last] || f.Dirty[1][last+1] {
		t.Errorf("time does not end at col %d", last)
	}
}

func TestRenderPomodoroPausedDimsColor(t *testing.T) {
	fc := RGB{0xff, 0x40, 0x20}
	f := RenderPomodoro(PomodoroView{Phase: "focus", Paused: true, RemainingSec: 1500, PlannedSec: 1500, FocusColor: fc})
	want := RGB{fc.R / 2, fc.G / 2, fc.B / 2}
	if !f.Dirty[1][pomoTimeX] || f.Pixels[1][pomoTimeX] != want {
		t.Fatalf("paused time color = %+v, want dimmed %+v", f.Pixels[1][pomoTimeX], want)
	}
}

func TestPomodoroPayloadUsesBuiltinIcon(t *testing.T) {
	focus := PomodoroPayload(PomodoroView{Phase: "focus", RemainingSec: 1499, PlannedSec: 1500}, 30)
	if focus["icon"] != "29802" {
		t.Errorf("focus icon = %v, want 29802 (tomato)", focus["icon"])
	}
	if focus["text"] != "24:59" {
		t.Errorf("text = %v, want 24:59", focus["text"])
	}
	if _, hasDraw := focus["draw"]; hasDraw {
		t.Error("pomodoro payload should be icon+text, no draw")
	}
	// With the built-in icon, the firmware centres the text after the icon — we
	// must NOT set textOffsetX/center (that double-shifts + clips the last digit).
	// MM:SS always fits, so the scroll object pins it outright.
	if got, ok := focus["scroll"].(map[string]any); !ok || got["mode"] != "static" {
		t.Errorf("scroll = %v, want {\"mode\":\"static\"}", focus["scroll"])
	}
	if _, has := focus["textOffsetX"]; has {
		t.Errorf("textOffsetX must be unset (icon field auto-places text); got %v", focus["textOffsetX"])
	}
	if _, has := focus["textCenter"]; has {
		t.Errorf("center must be unset (firmware centres after the icon); got %v", focus["textCenter"])
	}
	if pr, ok := focus["progress"].(int); !ok || pr < 99 || pr > 100 {
		t.Errorf("progress = %v, want ~100", focus["progress"])
	}
	if focus["progressTrackColor"] != "#222222" {
		t.Errorf("progressTrackColor = %v, want #222222 (dim track)", focus["progressTrackColor"])
	}
	if focus["lifetimeMs"] != 30_000 {
		t.Errorf("lifetimeMs = %v, want 30000", focus["lifetimeMs"])
	}
	// The Pomodoro tile owns the screen for its whole lifetime.
	assertHeld(t, focus, 30)
	brk := PomodoroPayload(PomodoroView{Phase: "short_break", RemainingSec: 300, PlannedSec: 300}, 30)
	if brk["icon"] != "6396" {
		t.Errorf("break icon = %v, want 6396 (coffee)", brk["icon"])
	}
	long := PomodoroPayload(PomodoroView{Phase: "long_break", RemainingSec: 900, PlannedSec: 900}, 30)
	if long["icon"] != "6396" {
		t.Errorf("long-break icon = %v, want 6396 (coffee)", long["icon"])
	}
}

func TestPomodoroPayloadPausedDimsColour(t *testing.T) {
	on := PomodoroPayload(PomodoroView{Phase: "focus", RemainingSec: 600, PlannedSec: 1500}, 30)
	off := PomodoroPayload(PomodoroView{Phase: "focus", Paused: true, RemainingSec: 600, PlannedSec: 1500}, 30)
	if on["textColor"] == off["textColor"] {
		t.Errorf("paused must dim the colour; both = %v", on["textColor"])
	}
	if off["progressColor"] != off["textColor"] {
		t.Errorf("progressColor should match the (dimmed) textColor when paused")
	}
	// The native icon keeps animating at full brightness, so dimming alone is a
	// weak cue: a paused countdown also fades in and out.
	if got, ok := off["textFadeMs"].(int); !ok || got <= 0 {
		t.Errorf("paused textFadeMs = %v, want a positive period", off["textFadeMs"])
	}
	if _, has := on["textFadeMs"]; has {
		t.Errorf("running countdown must not fade; textFadeMs = %v", on["textFadeMs"])
	}
}
