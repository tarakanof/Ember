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
	// "25:00": first digit '2' top-left pixel sits at the time origin (9,1).
	if !f.Dirty[1][9] || f.Pixels[1][9] != fc {
		t.Fatalf("time digit at (9,1) not painted in focus color; dirty=%v px=%+v", f.Dirty[1][9], f.Pixels[1][9])
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
	if w := countRowColor(full, 7, fc); w != 32 {
		t.Fatalf("full progress width = %d, want 32", w)
	}
	half := RenderPomodoro(PomodoroView{Phase: "focus", RemainingSec: 750, PlannedSec: 1500, FocusColor: fc})
	if w := countRowColor(half, 7, fc); w != 16 {
		t.Fatalf("half progress width = %d, want 16", w)
	}
	none := RenderPomodoro(PomodoroView{Phase: "focus", RemainingSec: 0, PlannedSec: 1500, FocusColor: fc})
	if w := countRowColor(none, 7, fc); w != 0 {
		t.Fatalf("empty progress width = %d, want 0", w)
	}
}

func TestRenderPomodoroBreakUsesBreakColor(t *testing.T) {
	bc := RGB{0x2e, 0xe8, 0x5e}
	f := RenderPomodoro(PomodoroView{Phase: "short_break", RemainingSec: 300, PlannedSec: 300, BreakColor: bc})
	if !f.Dirty[1][9] || f.Pixels[1][9] != bc {
		t.Fatalf("break time digit not painted in break color; px=%+v", f.Pixels[1][9])
	}
}

func TestRenderPomodoroBreakCupIsGrayNotBreakColor(t *testing.T) {
	bc := RGB{0x2e, 0xe8, 0x5e} // green
	f := RenderPomodoro(PomodoroView{Phase: "short_break", RemainingSec: 300, PlannedSec: 300, BreakColor: bc})
	// The mug rim (row 2, cols 1..5) must be the neutral gray, never the break colour.
	cupPixels := 0
	for x := 1; x <= 5; x++ {
		if !f.Dirty[2][x] {
			t.Fatalf("mug rim pixel (%d,2) not painted", x)
		}
		if f.Pixels[2][x] == bc {
			t.Fatalf("mug rim pixel (%d,2) painted in break colour; want gray", x)
		}
		if f.Pixels[2][x] == pomoCupGray {
			cupPixels++
		}
	}
	if cupPixels != 5 {
		t.Fatalf("gray mug rim pixels = %d, want 5", cupPixels)
	}
}

func TestRenderPomodoroPausedDimsColor(t *testing.T) {
	fc := RGB{0xff, 0x40, 0x20}
	f := RenderPomodoro(PomodoroView{Phase: "focus", Paused: true, RemainingSec: 1500, PlannedSec: 1500, FocusColor: fc})
	want := RGB{fc.R / 2, fc.G / 2, fc.B / 2}
	if !f.Dirty[1][9] || f.Pixels[1][9] != want {
		t.Fatalf("paused time color = %+v, want dimmed %+v", f.Pixels[1][9], want)
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
	// must NOT set textOffset/center (that double-shifts + clips the last digit).
	if focus["noScroll"] != true {
		t.Errorf("noScroll = %v, want true", focus["noScroll"])
	}
	if _, has := focus["textOffset"]; has {
		t.Errorf("textOffset must be unset (icon field auto-places text); got %v", focus["textOffset"])
	}
	if _, has := focus["center"]; has {
		t.Errorf("center must be unset (firmware centres after the icon); got %v", focus["center"])
	}
	if pr, ok := focus["progress"].(int); !ok || pr < 99 || pr > 100 {
		t.Errorf("progress = %v, want ~100", focus["progress"])
	}
	if focus["progressBC"] != "#222222" {
		t.Errorf("progressBC = %v, want #222222 (dim track)", focus["progressBC"])
	}
	if focus["lifetime"] != 30 {
		t.Errorf("lifetime = %v, want 30", focus["lifetime"])
	}
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
	if on["color"] == off["color"] {
		t.Errorf("paused must dim the colour; both = %v", on["color"])
	}
	if on["color"] != off["color"] && off["progressC"] != off["color"] {
		t.Errorf("progressC should match the (dimmed) colour when paused")
	}
}
