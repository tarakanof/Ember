package devices

import (
	"reflect"
	"slices"
	"testing"
)

func TestLegacyCapsTable(t *testing.T) {
	base := []string{"bot", "pomodoro", "weather"}
	withNP := []string{"bot", "pomodoro", "weather", "nowplaying"}
	limits := &CapsLimits{ViewBytes: 16383, ConfigBytes: 1024}
	cases := []struct {
		fw       string
		pages    []string
		features []string
		limits   *CapsLimits
	}{
		{"", base, []string{}, nil},
		{"dev", base, []string{}, nil},
		{"0.9", base, []string{}, nil},
		{"0.6.9", base, []string{}, nil},
		{"0.7.0", base, []string{"stats_intervals"}, nil},
		{"0.8.0", base, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.0-rc.1", base, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.0", withNP, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.5", withNP, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.6", withNP, []string{"stats_intervals", "view_wait", "np_control"}, nil},
		{"0.9.13", withNP, []string{"stats_intervals", "view_wait", "np_control"}, nil},
		{"0.9.14", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump"}, nil},
		{"0.9.16", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, nil},
		{"0.9.27", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, nil},
		{"0.9.28", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, limits},
		{"0.9.41", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, limits},
		{"0.10.0", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, limits},
		{"0.9.41+local", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, limits},
		{"0.9.0+build.7", withNP, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.0-rc.1+build.7", base, []string{"stats_intervals", "view_wait"}, nil},
	}
	for _, c := range cases {
		t.Run(c.fw, func(t *testing.T) {
			got := legacyCaps(c.fw)
			want := Caps{View: []int{1, 1}, Pages: c.pages, Features: c.features, Limits: c.limits}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("legacyCaps(%q) = %+v, want %+v", c.fw, got, want)
			}
			if err := got.Validate(); err != nil {
				t.Errorf("legacy caps for %q do not validate: %v", c.fw, err)
			}
		})
	}
}

func TestLegacyCapsTableIsOrderedAndUsesKnownTokens(t *testing.T) {
	known := []string{FeatureViewWait, featureNPControl, featureOTARollback, featureCoredump, featureStatsIntervals}
	for i, row := range legacyKnobCaps {
		if !SemverPattern.MatchString(row.minFW) {
			t.Errorf("row %d: %q is not a version", i, row.minFW)
		}
		if i > 0 && CompareSemver(legacyKnobCaps[i-1].minFW, row.minFW) >= 0 {
			t.Errorf("row %d (%s) is not after row %d (%s)", i, row.minFW, i-1, legacyKnobCaps[i-1].minFW)
		}
		for _, f := range row.features {
			if !slices.Contains(known, f) {
				t.Errorf("row %s: unknown feature token %q", row.minFW, f)
			}
		}
		for _, p := range row.pages {
			if !slices.Contains(KnobPageIDs, p) {
				t.Errorf("row %s: unknown page %q", row.minFW, p)
			}
		}
	}
}

func TestClockRecordHasNoEffectiveCaps(t *testing.T) {
	d := Record{ID: "clock-abcdef", Kind: KindClock}
	if v := d.view(); v.EffectiveCaps != nil {
		t.Fatalf("clock effective_caps = %+v, want none", v.EffectiveCaps)
	}
}
