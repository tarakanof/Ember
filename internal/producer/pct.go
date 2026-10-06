package producer

import "math"

func Pct(f float64) int {
	return min(max(int(math.Round(f)), 0), 100)
}
