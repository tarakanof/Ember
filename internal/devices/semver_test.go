package devices

import "testing"

func TestCompareSemver(t *testing.T) {
	ordered := []string{"0.9.2", "0.9.13", "0.9.14-1", "0.9.14-alpha", "0.9.14-alpha.1", "0.9.14-beta", "0.9.14", "0.10.0", "1.0.0"}
	for i := range ordered {
		for j := range ordered {
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := CompareSemver(ordered[i], ordered[j]); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}
