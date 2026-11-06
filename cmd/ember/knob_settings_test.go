package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/tarakanof/ember/internal/devices"
)

func TestKnobQuietDefaultsCalmAndNightLevel(t *testing.T) {
	d := devices.DefaultKnobSettings()
	if d.Quiet.Calm == nil || !*d.Quiet.Calm || d.Quiet.DimLevel == nil || *d.Quiet.DimLevel != (BrightnessConfig{}).resolved().NightLevel {
		t.Fatalf("defaults: %+v", d.Quiet)
	}
	var s devices.KnobSettings
	if err := json.Unmarshal([]byte(`{"bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.FillDefaults()
	if !reflect.DeepEqual(s.Quiet, d.Quiet) {
		t.Fatalf("a stored record without quiet reads %+v, want the defaults", s.Quiet)
	}
}

const (
	legacyCfgSettingsMax = 1024
)
