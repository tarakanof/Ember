package producer

import "testing"

func TestPct(t *testing.T) {
	for in, want := range map[float64]int{-3: 0, 0: 0, 49.4: 49, 49.5: 50, 100: 100, 130.2: 100} {
		if got := Pct(in); got != want {
			t.Errorf("Pct(%v) = %d, want %d", in, got, want)
		}
	}
}
