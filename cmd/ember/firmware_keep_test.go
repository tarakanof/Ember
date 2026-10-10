package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(b), "firmware_kept_in_use") {
		t.Fatalf("guarded delete of another knob's build = %d %s", resp.StatusCode, b)
	}
}

func threeImageStore(t *testing.T) *firmwareStore {
	t.Helper()
	s := newFWStore(t)
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), firmwareChannelRelease)
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.15"}), firmwareChannelTest)
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.16"}), firmwareChannelTest)
	return s
}

func noHold(string) (bool, func()) { return false, func() {} }

func TestFirmwareGuardedDeleteIsAtomicWithAChannelChange(t *testing.T) {
	s := threeImageStore(t)
	if _, err := s.setChannel("0.9.15", firmwareChannelRelease); err != nil {
		t.Fatal(err)
	}
	if err := s.removeGuarded("0.9.15", nil, noHold, nil); !errors.Is(err, errFirmwareKeptNewest) {
		t.Fatalf("delete after the promotion = %v, want firmware_kept_newest", err)
	}

	s = threeImageStore(t)
	var chErr error
	done := make(chan struct{})
	hold := func(string) (bool, func()) {
		go func() { _, chErr = s.setChannel("0.9.15", firmwareChannelRelease); close(done) }()
		select {
		case <-done:
			t.Error("the promotion ran inside the guarded delete")
		case <-time.After(50 * time.Millisecond):
		}
		return false, func() {}
	}
	if err := s.removeGuarded("0.9.15", nil, hold, nil); err != nil {
		t.Fatalf("delete before the promotion = %v", err)
	}
	recvWithin(t, done, "promotion after the delete")
	if !errors.Is(chErr, errFirmwareNotFound) {
		t.Fatalf("promotion after the delete = %v, want not found", chErr)
	}

	for i := range 40 {
		s := threeImageStore(t)
		var wg sync.WaitGroup
		var chErr, rmErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, chErr = s.setChannel("0.9.15", firmwareChannelRelease) }()
		go func() { defer wg.Done(); rmErr = s.removeGuarded("0.9.15", nil, noHold, nil) }()
		wg.Wait()
		switch {
		case chErr == nil && errors.Is(rmErr, errFirmwareKeptNewest):
		case rmErr == nil && errors.Is(chErr, errFirmwareNotFound):
		default:
			t.Fatalf("run %d: channel err %v, remove err %v", i, chErr, rmErr)
		}
	}
}

func TestFirmwareGuardedDeleteHoldsCheckinsUntilThePurgeEnds(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.12"}), "")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.20"}), "?channel=release")
	k.idle(t)
	store := k.app.knobFW
	rename := store.rename
	var checkinErr error
	checkedIn := make(chan struct{})
	var once sync.Once
	store.rename = func(oldpath, newpath string) error {
		once.Do(func() {
			go func() {
				_, checkinErr = k.app.devices.checkin(k.knob.ID, deviceCheckin{FW: "0.9.12"}, nil, "")
				close(checkedIn)
			}()
			select {
			case <-checkedIn:
				t.Error("a checkin ran while the guarded delete was purging")
			case <-time.After(50 * time.Millisecond):
			}
		})
		return rename(oldpath, newpath)
	}
	resp, b := devReq(t, k.srv, "DELETE", "/v1/firmware/0.9.12?keep=protected", testToken, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("guarded delete = %d %s", resp.StatusCode, b)
	}
	recvWithin(t, checkedIn, "checkin after the delete")
	if checkinErr != nil {
		t.Fatal(checkinErr)
	}
	resp, b = devReq(t, k.srv, "DELETE", "/v1/firmware/0.9.20?keep=protected", testToken, "")
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(b), "firmware_kept_newest") {
		t.Fatalf("guarded delete of the newest release = %d %s", resp.StatusCode, b)
	}
}

func checkinWithin(t *testing.T, k otaKnob, fw string, wait time.Duration) (chan struct{}, *error) {
	t.Helper()
	var err error
	done := make(chan struct{})
	go func() {
		_, err = k.app.devices.checkin(k.knob.ID, deviceCheckin{FW: fw}, nil, "")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(wait):
	}
	return done, &err
}

func TestFirmwareGuardedDeletePanicReleasesTheRegistry(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.12"}), "")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.20"}), "?channel=release")
	k.idle(t)
	store := k.app.knobFW
	store.rename = func(string, string) error { panic("rename blew up") }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the purge did not panic")
			}
		}()
		_ = store.removeGuarded("0.9.12", k.app.devices.otaTargets, k.app.devices.holdUnlessKept, nil)
	}()
	done, err := checkinWithin(t, k, "0.9.12", 2*time.Second)
	select {
	case <-done:
		if *err != nil {
			t.Fatal(*err)
		}
	default:
		t.Fatal("a checkin after the panicked purge is still waiting on the registry")
	}
}

func registryFree(r *deviceRegistry) bool {
	if !r.mu.TryLock() {
		return false
	}
	r.mu.Unlock()
	return true
}

func noteOnce(ch chan bool, v bool) {
	select {
	case ch <- v:
	default:
	}
}

func TestFirmwarePruneHoldsTheRegistryOnlyWhileRetiring(t *testing.T) {
	k := newOTAKnob(t)
	k.idle(t)
	for i := 1; i <= firmwareKept; i++ {
		k.upload(t, fakeFirmware(fwOpts{version: fmt.Sprintf("0.9.%d", i)}), "")
	}
	store := k.app.knobFW
	rename, removeAll := store.rename, store.removeAll
	retiring, dropping := make(chan bool, 1), make(chan bool, 1)
	store.rename = func(oldpath, newpath string) error {
		if filepath.Base(oldpath) == "0.9.1" {
			noteOnce(retiring, registryFree(k.app.devices))
		}
		return rename(oldpath, newpath)
	}
	store.removeAll = func(path string) error {
		if strings.HasPrefix(filepath.Base(path), firmwareTempPrefix+"0.9.1-") {
			noteOnce(dropping, registryFree(k.app.devices))
		}
		return removeAll(path)
	}
	k.upload(t, fakeFirmware(fwOpts{version: fmt.Sprintf("0.9.%d", firmwareKept+1)}), "")
	if recvWithin(t, retiring, "the retire of 0.9.1") {
		t.Error("retention retired 0.9.1 without holding the registry")
	}
	if !recvWithin(t, dropping, "the removal of the retired 0.9.1") {
		t.Error("the registry stayed held while the retired copy was removed")
	}
	done, err := checkinWithin(t, k, "0.9.1", 2*time.Second)
	recvWithin(t, done, "a checkin after the prune")
	if *err != nil {
		t.Fatal(*err)
	}
	if _, ok := store.get("0.9.1"); ok {
		t.Fatal("0.9.1 not evicted")
	}
}
