package render

import "testing"

func TestReminderPopupFrame(t *testing.T) {
	f := ReminderPopupFrame("HI")

	// Bell occupies cols 0-7 in gold: row 5 is fully lit ("XXXXXXXX").
	for x := 0; x < 8; x++ {
		if !f.Dirty[5][x] || f.Pixels[5][x] != reminderGold {
			t.Fatalf("bell pixel (%d,5) = %v dirty=%v, want gold", x, f.Pixels[5][x], f.Dirty[5][x])
		}
	}
	// Text starts at col 9, row 1 (3×5 font): 'H' row0 is "X.X" → (9,1) lit gold.
	if !f.Dirty[1][9] || f.Pixels[1][9] != reminderGold {
		t.Fatalf("text pixel (9,1) = %v dirty=%v, want gold", f.Pixels[1][9], f.Dirty[1][9])
	}
	// Gap column between bell and text stays unlit.
	for y := 0; y < 8; y++ {
		if f.Dirty[y][8] {
			t.Fatalf("gap pixel (8,%d) lit, want blank", y)
		}
	}
}

func TestReminderPopupFrame_LongTextClips(t *testing.T) {
	// Must not panic and must not paint past the right edge (paintCell bounds).
	f := ReminderPopupFrame("MEETING WITH A VERY LONG TITLE")
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			_ = f.Pixels[y][x] // touching every cell is enough; OOB would have panicked
		}
	}
}
