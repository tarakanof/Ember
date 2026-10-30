package devices

import "testing"

const testClockUID = "e868e705ffb8"

func TestOTASkipsClockRecords(t *testing.T) {
	r := New(func() KV { return nil })
	if _, _, err := r.SeenClock(testClockUID, nil, "d"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	d := &r.state.Devices[0]
	d.OTA = &KnobOTA{Target: "0.9.0", Version: "0.9.0", Phase: OTAPhaseDownloading}
	d.LastCheckin = &Checkin{FW: "0.9.0"}
	r.mu.Unlock()
	if r.OTATargets("0.9.0") {
		t.Error("otaTargets counted a clock record")
	}
	if kept, release := r.HoldUnlessKept("0.9.0"); kept {
		t.Error("holdUnlessKept counted a clock record")
	} else {
		release()
	}
	d.OTA.Phase = OTAPhaseFailed
	d.OTA.Blocked = []string{"0.9.0"}
	epoch := r.Epoch()
	if err := r.OTARetire([]string{"0.9.0"}, ""); err != nil {
		t.Fatal(err)
	}
	if r.Epoch() != epoch || len(r.state.Devices[0].OTA.Blocked) != 1 {
		t.Error("otaRetire touched a clock record")
	}
}
