package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func knobConfigSwipePages(t *testing.T, f *viewFixture) *bool {
	t.Helper()
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/"+f.m.ID+"/config", testToken, "")
	mustOK(t, "config get", resp, b)
	var cfg struct {
		SwipePages *bool `json:"swipe_pages"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.SwipePages
}

func TestKnobSwipePagesRoundTripsOverHTTP(t *testing.T) {
	f := newViewFixture(t)
	if got := knobConfigSwipePages(t, f); got == nil || !*got {
		t.Fatalf("default swipe_pages = %v, want true", got)
	}
	putKnobConfig(t, f.srv, f.m.ID, `{"swipe_pages":false}`)
	if got := knobConfigSwipePages(t, f); got == nil || *got {
		t.Fatalf("swipe_pages after PUT false = %v", got)
	}
	putKnobConfig(t, f.srv, f.m.ID, `{"poll_ms":3000}`)
	if got := knobConfigSwipePages(t, f); got == nil || *got {
		t.Fatalf("swipe_pages after a partial PUT = %v, want false kept", got)
	}
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/self/config", f.m.Token, "")
	mustOK(t, "self config", resp, b)
	var self struct {
		Config struct {
			SwipePages *bool `json:"swipe_pages"`
		} `json:"config"`
	}
	if err := json.Unmarshal(b, &self); err != nil || self.Config.SwipePages == nil || *self.Config.SwipePages {
		t.Fatalf("knob config = %s, want swipe_pages false", b)
	}
}

func TestKnobSwipePagesRejectsANonBool(t *testing.T) {
	f := newViewFixture(t)
	for _, body := range []string{`{"swipe_pages":"no"}`, `{"swipe_pages":0}`} {
		resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d %s, want 400", body, resp.StatusCode, b)
		}
	}
	if got := knobConfigSwipePages(t, f); got == nil || !*got {
		t.Fatalf("swipe_pages after rejected PUTs = %v, want true", got)
	}
}
