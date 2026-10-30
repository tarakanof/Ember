package devices

import (
	"encoding/json"
	"testing"
	"time"
)

func TestKnobRebootedReadsUptimeAndBoots(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	at := func(seen time.Duration, up, boots int64) Checkin {
		c := Checkin{SeenAt: t0.Add(seen), UptimeS: up}
		if boots >= 0 {
			c.Diag = &Diag{Boots: boots}
		}
		return c
	}
	for _, tc := range []struct {
		name      string
		prev, cur Checkin
		want      bool
	}{
		{"uptime grew, same boot", at(0, 100, 7), at(30*time.Second, 130, 7), false},
		{"uptime fell, no diag", at(0, 100, -1), at(10*time.Second, 4, -1), true},
		{"uptime fell, same boots", at(0, 100, 7), at(10*time.Second, 4, 7), false},
		{"uptime grew, boots moved", at(0, 2, 7), at(10*time.Second, 5, 8), true},
		{"boots fell: an older checkin", at(0, 30, 8), at(time.Second, 800, 7), false},
		{"boots unknown", at(0, 2, 0), at(10*time.Second, 5, 8), false},
		{"diag dropped", at(0, 2, 7), at(10*time.Second, 5, -1), false},
		{"diag dropped, uptime fell", at(0, 100, 7), at(10*time.Second, 4, -1), true},
		{"boots 0 on one side, uptime fell", at(0, 100, 7), at(10*time.Second, 4, 0), true},
		{"boots 0 on one side, uptime grew", at(0, 100, 0), at(10*time.Second, 110, 7), false},
		{"boots fell after an NVS erase", at(0, 3000, 40), at(30*time.Second, 20, 1), true},
		{"uptime missing, no diag", at(0, 800, -1), at(10*time.Second, 0, -1), false},
		{"out of order, no diag", at(0, 830, -1), at(time.Second, 800, -1), false},
		{"previous record days old, no diag", at(0, 800, -1), at(72*time.Hour, 30, -1), true},
	} {
		if got := knobRebooted(&tc.prev, tc.cur); got != tc.want {
			t.Errorf("%s: knobRebooted = %v, want %v", tc.name, got, tc.want)
		}
	}
	if knobRebooted(nil, at(0, 1, 1)) {
		t.Error("a first checkin reads as a reboot")
	}
}

const testHwID = "3cdc7561fc8c"

func TestCheckinResultKeepsServerFieldsOffTheWire(t *testing.T) {
	blob, err := json.Marshal(CheckinResult{
		ConfigVersion: 3,
		NewCrash:      &Crash{},
		BootedAt:      time.Unix(1, 0),
		Stale:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(blob), `{"config_version":3}`; got != want {
		t.Fatalf("checkin reply = %s, want %s", got, want)
	}
}
