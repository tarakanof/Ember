package producer

import "math"

// Pct rounds a percentage to an int clamped to 0..100.
func Pct(f float64) int {
	return min(max(int(math.Round(f)), 0), 100)
}
