package render

import "testing"

func TestReminderPopupFrame(t *testing.T) {
	f := ReminderPopupFrame("HI")

	for x := 0; x < 8; x++ {
		if !f.Dirty[5][x] || f.Pixels[5][x] != reminderGold {
			t.Fatalf("bell pixel (%d,5) = %v dirty=%v, want gold", x, f.Pixels[5][x], f.Dirty[5][x])
		}
	}
	if !f.Dirty[1][9] || f.Pixels[1][9] != reminderGold {
		t.Fatalf("text pixel (9,1) = %v dirty=%v, want gold", f.Pixels[1][9], f.Dirty[1][9])
	}
	for y := 0; y < 8; y++ {
		if f.Dirty[y][8] {
			t.Fatalf("gap pixel (8,%d) lit, want blank", y)
		}
	}
}

func TestReminderPopupPinsText(t *testing.T) {
	assertPinnedText(t, ReminderPopupPayload("Call mom", "", 8, false))
	assertPinnedText(t, ReminderPopupPayload("Call mom", "1234", 8, true))
}

func TestReminderPopupRepeatsUnlessHeld(t *testing.T) {
	if p := ReminderPopupPayload("Call mom about the weekend", "", 8, false); p["repeat"] != 1 {
		t.Errorf("unheld repeat = %v, want 1", p["repeat"])
	}
	if p := ReminderPopupPayload("Call mom about the weekend", "", 8, true); p["repeat"] != nil {
		t.Errorf("held repeat = %v, want absent", p["repeat"])
	}
}

func TestReminderPopupFrame_LongTextClips(t *testing.T) {
	f := ReminderPopupFrame("MEETING WITH A VERY LONG TITLE")
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			_ = f.Pixels[y][x]
		}
	}
}
