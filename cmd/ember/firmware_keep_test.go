package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestFirmwareNewestKept(t *testing.T) {
	idx := func(images ...string) map[string]firmwareMeta {
		out := map[string]firmwareMeta{}
		for _, im := range images {
			v, ch, _ := strings.Cut(im, "/")
			out[v] = firmwareMeta{firmwareImage: firmwareImage{Version: v, Channel: ch}}
		}
		return out
	}
	cases := []struct {
		name  string
		index map[string]firmwareMeta
		want  []string
	}{
		{"empty", idx(), nil},
		{"release only", idx("0.9.9/release", "0.9.10/release"), []string{"0.9.10"}},
		{"newer test", idx("0.9.10/release", "0.9.11-rc1/test", "0.9.9/test"), []string{"0.9.10", "0.9.11-rc1"}},
		{"older test", idx("0.9.10/release", "0.9.9/test"), []string{"0.9.10"}},
		{"test only", idx("0.9.9/test", "0.9.10/test"), []string{"0.9.10"}},
	}
	for _, c := range cases {
		if got := firmwareNewestKept(c.index); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFirmwareGuardedDeleteRefusesProtectedVersions(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.11"}), "")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.13"}), "")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.15"}), "?channel=release")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.16"}), "")
	k.idle(t)
	del := func(version, query string) (int, string) {
		resp, b := devReq(t, k.srv, "DELETE", "/v1/firmware/"+version+query, testToken, "")
		return resp.StatusCode, string(b)
	}
	for _, v := range []string{"0.9.13", "0.9.15", "0.9.16"} {
		if code, b := del(v, "?keep=protected"); code != http.StatusConflict || !strings.Contains(b, "firmware_kept") {
			t.Errorf("guarded delete of %s = %d %s, want 409 firmware_kept", v, code, b)
		}
	}
	k.target(t, "0.9.14")
	if code, b := del("0.9.14", "?keep=protected"); code != http.StatusConflict || !strings.Contains(b, "update target") {
		t.Errorf("guarded delete of the target = %d %s, want 409", code, b)
	}
	k.put(t, `{"target":null}`)
	if code, b := del("0.9.14", "?keep=protected"); code != http.StatusNoContent {
		t.Errorf("guarded delete of an old build = %d %s", code, b)
	}
	if code, _ := del("0.9.11", "?keep=bogus"); code != http.StatusBadRequest {
		t.Errorf("unknown keep = %d, want 400", code)
	}
	if code, b := del("0.9.11", "?keep=protected"); code != http.StatusNoContent {
		t.Errorf("guarded delete of an old build = %d %s", code, b)
	}
	if code, b := del("0.9.11", "?keep=protected"); code != http.StatusNotFound {
		t.Errorf("guarded delete of a gone build = %d %s", code, b)
	}
	for _, v := range []string{"0.9.13", "0.9.15", "0.9.16"} {
		if code, b := del(v, ""); code != http.StatusNoContent {
			t.Errorf("unguarded delete of %s = %d %s, want 204", v, code, b)
		}
	}
}

func TestFirmwareGuardedDeleteKeepsWhatAnotherKnobRuns(t *testing.T) {
	k := newOTAKnob(t)
	resp, b := devReq(t, k.srv, "POST", "/v1/devices", testToken, `{"kind":"cinder-knob","hw_id":"3cdc75000002","name":"Shelf knob"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint = %d %s", resp.StatusCode, b)
	}
	var minted mintResp
	if err := json.Unmarshal(b, &minted); err != nil {
		t.Fatal(err)
	}
	other := otaKnob{app: k.app, srv: k.srv, knob: minted}
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.12"}), "")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.20"}), "?channel=release")
	k.idle(t)
	other.checkin(t, otaReport("0.9.12", runningBuild, "valid", "idle", true, ""))
	resp, b = devReq(t, k.srv, "DELETE", "/v1/firmware/0.9.12?keep=protected", testToken, "")
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(b), "a knob runs") {
		t.Fatalf("guarded delete of another knob's build = %d %s", resp.StatusCode, b)
	}
}

func TestFirmwareGuardedDeleteIsAtomicWithAChannelChange(t *testing.T) {
	for i := range 40 {
		s := newFWStore(t)
		putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), firmwareChannelRelease)
		putFW(t, s, fakeFirmware(fwOpts{version: "0.9.15"}), firmwareChannelTest)
		putFW(t, s, fakeFirmware(fwOpts{version: "0.9.16"}), firmwareChannelTest)
		var wg sync.WaitGroup
		var chErr, rmErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, chErr = s.setChannel("0.9.15", firmwareChannelRelease) }()
		go func() {
			defer wg.Done()
			rmErr = s.removeGuarded("0.9.15", nil, func(string) bool { return false }, nil)
		}()
		wg.Wait()
		switch {
		case chErr == nil && errors.Is(rmErr, errFirmwareKept):
		case rmErr == nil && errors.Is(chErr, errFirmwareNotFound):
		default:
			t.Fatalf("run %d: channel err %v, remove err %v", i, chErr, rmErr)
		}
	}
}
