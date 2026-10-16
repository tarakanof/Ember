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

func TestKnobSwipePagesDefaultsOn(t *testing.T) {
	d := defaultKnobSettings()
	if d.SwipePages == nil || !*d.SwipePages {
		t.Fatalf("default swipe_pages = %v, want on", d.SwipePages)
	}
}

func TestKnobSwipePagesStoredWithoutKeyReadsOn(t *testing.T) {
	var s knobSettings
	if err := json.Unmarshal([]byte(`{"pages":[{"id":"bot","on":true}],"home":"bot","bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.fillDefaults()
	if s.SwipePages == nil || !*s.SwipePages {
		t.Fatal("a stored record without swipe_pages reads it on")
	}
}

func TestKnobSwipePagesCloneDoesNotShareThePointer(t *testing.T) {
	cur := defaultKnobSettings()
	c := cur.clone()
	*c.SwipePages = false
	if !*cur.SwipePages {
		t.Fatal("clone shares swipe_pages with the original")
	}
}

func TestKnobSwipePagesMergeFalseAndNull(t *testing.T) {
	cur := defaultKnobSettings()
	off, err := mergeKnobSettings(cur.clone(), []byte(`{"swipe_pages":false}`))
	if err != nil || off.SwipePages == nil || *off.SwipePages || !*cur.SwipePages {
		t.Fatalf("merge false: %v %v (original %v)", err, off.SwipePages, *cur.SwipePages)
	}
	kept, err := mergeKnobSettings(off.clone(), []byte(`{"poll_ms":3000}`))
	if err != nil || kept.SwipePages == nil || *kept.SwipePages {
		t.Fatalf("a partial PUT dropped swipe_pages false: %v %v", err, kept.SwipePages)
	}
	reset, err := mergeKnobSettings(off.clone(), []byte(`{"swipe_pages":null}`))
	if err != nil || reset.SwipePages == nil || !*reset.SwipePages {
		t.Fatalf("swipe_pages null = %v %v, want on", err, reset.SwipePages)
	}
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
