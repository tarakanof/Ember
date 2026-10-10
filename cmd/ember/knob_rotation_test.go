package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func knobConfigRotation(t *testing.T, f *viewFixture) int {
	t.Helper()
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/"+f.m.ID+"/config", testToken, "")
	mustOK(t, "config get", resp, b)
	var cfg struct {
		Rotation *int `json:"rotation"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil || cfg.Rotation == nil {
		t.Fatalf("config without rotation: %v %s", err, b)
	}
	return *cfg.Rotation
}

func TestKnobRotationRoundTripsAndReachesTheKnob(t *testing.T) {
	f := newViewFixture(t)
	if got := knobConfigRotation(t, f); got != 0 {
		t.Fatalf("default rotation = %d, want 0", got)
	}
	_, version, err := f.app.devices.versions(f.m.ID)
	if err != nil {
		t.Fatal(err)
	}
	putKnobConfig(t, f.srv, f.m.ID, `{"rotation":180}`)
	if got := knobConfigRotation(t, f); got != 180 {
		t.Fatalf("rotation after PUT = %d, want 180", got)
	}
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, capsCheckinBody(t, "0.10.0", version, nil))
	mustOK(t, "checkin", resp, b)
	var reply struct {
		Config *struct {
			Rotation *int `json:"rotation"`
		} `json:"config"`
	}
	if err := json.Unmarshal(b, &reply); err != nil || reply.Config == nil || reply.Config.Rotation == nil || *reply.Config.Rotation != 180 {
		t.Fatalf("checkin reply = %s, want config.rotation 180", b)
	}
	resp, b = devReq(t, f.srv, "GET", "/v1/devices/self/config", f.m.Token, "")
	if mustOK(t, "self config", resp, b); !strings.Contains(string(b), `"rotation":180`) {
		t.Fatalf("self config = %s, want rotation 180", b)
	}
}

func TestKnobRotationRejectsInvalidValues(t *testing.T) {
	f := newViewFixture(t)
	for _, body := range []string{`{"rotation":45}`, `{"rotation":360}`, `{"rotation":-90}`, `{"rotation":"180"}`, `{"rotation":90.5}`} {
		resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PUT %s = %d %s, want 400", body, resp.StatusCode, b)
		}
	}
	if got := knobConfigRotation(t, f); got != 0 {
		t.Fatalf("rotation after rejected PUTs = %d, want 0", got)
	}
}

func TestCapsRotationsPresentAndMissing(t *testing.T) {
	f := newViewFixture(t)
	if c := knobEffectiveCaps(t, f); !reflect.DeepEqual(c["rotations"], []any{0.0}) {
		t.Fatalf("legacy effective_caps rotations = %v, want [0]", c["rotations"])
	}
	reply := postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "rotations": []int{180, 0}})
	if reply["caps_ack"] != true {
		t.Fatalf("reply = %v, want caps_ack", reply)
	}
	if c := knobEffectiveCaps(t, f); c["source"] != capsSourceReported || !reflect.DeepEqual(c["rotations"], []any{0.0, 180.0}) {
		t.Fatalf("effective_caps = %v, want rotations [0 180]", c)
	}
	for _, caps := range []map[string]any{
		{"view": []int{1, 1}, "pages": []string{"bot"}},
		{"view": []int{1, 1}, "pages": []string{"bot"}, "rotations": []int{}},
	} {
		if reply := postCapsCheckin(t, f, "0.10.0", caps); reply["caps_ack"] != true {
			t.Fatalf("reply = %v, want caps_ack", reply)
		}
		if c := knobEffectiveCaps(t, f); c["source"] != capsSourceReported || !reflect.DeepEqual(c["rotations"], []any{0.0}) {
			t.Fatalf("effective_caps for caps %v = %v, want rotations [0]", caps, c)
		}
	}
}

func TestCapsKnobConfigPutRejectsRotationOutsideCaps(t *testing.T) {
	f := newViewFixture(t)
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "pomodoro", "weather"}})
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"rotation":180}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "caps.rotations") {
		t.Fatalf("PUT rotation outside caps = %d %s, want 400 naming caps.rotations", resp.StatusCode, b)
	}
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "pomodoro", "weather"}, "rotations": []int{0, 180}})
	putKnobConfig(t, f.srv, f.m.ID, `{"rotation":180}`)
	resp, b = devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"rotation":90}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT rotation 90 with caps [0 180] = %d %s, want 400", resp.StatusCode, b)
	}
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "pomodoro", "weather"}})
	putKnobConfig(t, f.srv, f.m.ID, `{"poll_ms":3000}`)
	putKnobConfig(t, f.srv, f.m.ID, `{"rotation":0}`)
}

func TestLegacyKnobConfigPutIsNotCheckedForRotation(t *testing.T) {
	f := newViewFixture(t)
	postCapsCheckin(t, f, "0.9.41", nil)
	putKnobConfig(t, f.srv, f.m.ID, `{"rotation":270}`)
}
