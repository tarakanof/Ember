package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFirmwareUploadAnswers(t *testing.T) {
	k := newOTAKnob(t)
	img := fakeFirmware(fwOpts{version: "0.9.14"})
	resp, b := rawReq(t, k.srv, "POST", "/v1/firmware", testToken, img, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first upload = %d: %s", resp.StatusCode, b)
	}
	var got firmwareImage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Channel != "test" || got.Project != "cinder" || got.IDFVer != "v5.5.5" || got.Version != "0.9.14" || got.ELF {
		t.Fatalf("image = %+v", got)
	}
	keys := []string{`"build"`, `"channel"`, `"elf"`, `"idf_ver"`, `"project"`, `"sha256"`, `"size"`, `"uploaded_at"`, `"version"`}
	last := -1
	for _, key := range keys {
		i := strings.Index(string(b), key)
		if i <= last {
			t.Fatalf("keys not sorted at %s: %s", key, b)
		}
		last = i
	}
	if resp, _ := rawReq(t, k.srv, "POST", "/v1/firmware", testToken, img, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("same bytes = %d, want 200", resp.StatusCode)
	}
	other := fakeFirmware(fwOpts{version: "0.9.14", seed: 8})
	if resp, _ := rawReq(t, k.srv, "POST", "/v1/firmware", testToken, other, nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("other bytes = %d, want 409", resp.StatusCode)
	}
	k.idle(t)
	k.target(t, "0.9.14")
	if resp, _ := rawReq(t, k.srv, "POST", "/v1/firmware?replace=1", testToken, other, nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("replace a target = %d, want 409", resp.StatusCode)
	}
	k.put(t, `{"target":null}`)
	if resp, _ := rawReq(t, k.srv, "POST", "/v1/firmware?replace=1", testToken, other, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("replace = %d, want 201", resp.StatusCode)
	}
	if resp, _ := rawReq(t, k.srv, "POST", "/v1/firmware?channel=beta", testToken, img, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad channel = %d", resp.StatusCode)
	}
	big := make([]byte, firmwareMaxBytes+1)
	if resp, _ := rawReq(t, k.srv, "POST", "/v1/firmware", testToken, big, nil); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big = %d, want 413", resp.StatusCode)
	}
}

func TestFirmwareUploadRejectsBadImages(t *testing.T) {
	k := newOTAKnob(t)
	withToken := fakeFirmware(fwOpts{version: "0.9.14"})
	copy(withToken[50000:], testToken)
	withMarker := fakeFirmware(fwOpts{version: "0.9.14"})
	copy(withMarker[50000:], firmwareDevSeedMarker)
	for name, c := range map[string]struct {
		img  []byte
		want string
	}{
		"garbage":     {bytes.Repeat([]byte{1}, 70000), "bad_image"},
		"esp32":       {fakeFirmware(fwOpts{version: "0.9.14", chip: 0x05}), "wrong_chip"},
		"other app":   {fakeFirmware(fwOpts{version: "0.9.14", project: "hello"}), "wrong_project"},
		"no semver":   {fakeFirmware(fwOpts{version: "dev"}), "bad_version"},
		"ember token": {withToken, "dev_seed_build"},
		"seed marker": {withMarker, "dev_seed_build"},
	} {
		t.Run(name, func(t *testing.T) {
			resp, b := rawReq(t, k.srv, "POST", "/v1/firmware", testToken, c.img, nil)
			if resp.StatusCode != http.StatusBadRequest || strings.TrimSpace(string(b)) != `{"error":"`+c.want+`"}` {
				t.Fatalf("= %d %s, want 400 %s", resp.StatusCode, b, c.want)
			}
			if strings.Contains(string(b), testToken) {
				t.Fatal("token echoed")
			}
		})
	}
	resp, b := devReq(t, k.srv, "GET", "/v1/firmware", testToken, "")
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("list = %d %s", resp.StatusCode, b)
	}
}

func TestFirmwareELFAndDownloads(t *testing.T) {
	k := newOTAKnob(t)
	elf := fakeELF(4)
	img := fakeFirmware(fwOpts{version: "0.9.14", elf: elf})
	stored := k.upload(t, img, "?channel=release")
	if resp, _ := rawReq(t, k.srv, "GET", "/v1/firmware/0.9.14/elf", testToken, nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("elf before upload = %d", resp.StatusCode)
	}
	resp, b := rawReq(t, k.srv, "PUT", "/v1/firmware/0.9.14/elf", testToken, fakeELF(5), nil)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "elf_mismatch") {
		t.Fatalf("wrong elf = %d %s", resp.StatusCode, b)
	}
	withToken := append(bytes.Clone(elf), []byte(testToken)...)
	if resp, b := rawReq(t, k.srv, "PUT", "/v1/firmware/0.9.14/elf", testToken, withToken, nil); resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "dev_seed_build") {
		t.Fatalf("elf with token = %d %s", resp.StatusCode, b)
	}
	if resp, _ := rawReq(t, k.srv, "PUT", "/v1/firmware/0.9.99/elf", testToken, elf, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("elf for unknown version = %d", resp.StatusCode)
	}
	if resp, b := rawReq(t, k.srv, "PUT", "/v1/firmware/0.9.14/elf", testToken, elf, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("elf = %d %s", resp.StatusCode, b)
	}
	for path, c := range map[string]struct {
		body []byte
		name string
	}{
		"/v1/firmware/0.9.14/bin":                        {img, "cinder-0.9.14.bin"},
		"/v1/firmware/0.9.14/elf":                        {elf, "cinder-0.9.14.elf"},
		"/v1/firmware/by-build/" + stored.Build + "/elf": {elf, "cinder-0.9.14.elf"},
	} {
		resp, b := rawReq(t, k.srv, "GET", path, testToken, nil, nil)
		if resp.StatusCode != http.StatusOK || !bytes.Equal(b, c.body) {
			t.Fatalf("%s = %d, %d bytes", path, resp.StatusCode, len(b))
		}
		if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename="`+c.name+`"` {
			t.Fatalf("%s disposition = %q", path, got)
		}
	}
	for _, path := range []string{"/v1/firmware/by-build/00000000/elf", "/v1/firmware/by-build/XYZ/elf", "/v1/firmware/0.9.99/bin"} {
		if resp, _ := rawReq(t, k.srv, "GET", path, testToken, nil, nil); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
	var list []firmwareImage
	_, b = devReq(t, k.srv, "GET", "/v1/firmware", testToken, "")
	if err := json.Unmarshal(b, &list); err != nil || len(list) != 1 || !list[0].ELF {
		t.Fatalf("list = %s", b)
	}
}

func TestFirmwarePatchAndDelete(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	resp, b := devReq(t, k.srv, "PATCH", "/v1/firmware/0.9.14", testToken, `{"channel":"release"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"channel":"release"`) {
		t.Fatalf("patch = %d %s", resp.StatusCode, b)
	}
	for body, want := range map[string]int{`{"channel":"beta"}`: 400, `{"channel":"test","x":1}`: 400} {
		if resp, _ := devReq(t, k.srv, "PATCH", "/v1/firmware/0.9.14", testToken, body); resp.StatusCode != want {
			t.Fatalf("%s = %d", body, resp.StatusCode)
		}
	}
	if resp, _ := devReq(t, k.srv, "PATCH", "/v1/firmware/0.9.99", testToken, `{"channel":"test"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch unknown = %d", resp.StatusCode)
	}
	k.idle(t)
	k.target(t, "0.9.14")
	if resp, _ := devReq(t, k.srv, "DELETE", "/v1/firmware/0.9.14", testToken, ""); resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete target = %d, want 409", resp.StatusCode)
	}
	k.put(t, `{"target":null}`)
	if resp, _ := devReq(t, k.srv, "DELETE", "/v1/firmware/0.9.14", testToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp, _ := devReq(t, k.srv, "DELETE", "/v1/firmware/0.9.14", testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete again = %d", resp.StatusCode)
	}
}

func TestFirmwareRoutesByCredential(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.idle(t)
	admin := mintClient(t, k.srv, "ops", scopeAdmin).Token
	read := mintClient(t, k.srv, "reader", scopeRead, scopeIngest, scopeControl).Token
	routes := []struct{ method, path string }{
		{"GET", "/v1/firmware"},
		{"POST", "/v1/firmware"},
		{"PUT", "/v1/firmware/0.9.14/elf"},
		{"GET", "/v1/firmware/0.9.14/bin"},
		{"GET", "/v1/firmware/0.9.14/elf"},
		{"GET", "/v1/firmware/by-build/00000000/elf"},
		{"PATCH", "/v1/firmware/0.9.14"},
		{"DELETE", "/v1/firmware/0.9.15"},
		{"GET", "/v1/devices/" + k.knob.ID + "/ota"},
		{"PUT", "/v1/devices/" + k.knob.ID + "/ota"},
	}
	for _, rt := range routes {
		for _, c := range []struct {
			name, token string
			want        int
		}{
			{"none", "", http.StatusUnauthorized},
			{"knob", k.knob.Token, http.StatusUnauthorized},
			{"client without admin", read, http.StatusForbidden},
		} {
			if resp, _ := rawReq(t, k.srv, rt.method, rt.path, c.token, nil, nil); resp.StatusCode != c.want {
				t.Errorf("%s %s as %s = %d, want %d", rt.method, rt.path, c.name, resp.StatusCode, c.want)
			}
		}
		for _, tok := range []string{testToken, admin} {
			if resp, _ := rawReq(t, k.srv, rt.method, rt.path, tok, nil, nil); resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				t.Errorf("%s %s as owner/admin = %d", rt.method, rt.path, resp.StatusCode)
			}
		}
	}
	for _, tok := range []string{"", testToken, admin, read} {
		if resp, _ := rawReq(t, k.srv, "GET", "/v1/devices/self/firmware/0.9.14", tok, nil, nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("device download with a non-device token = %d, want 401", resp.StatusCode)
		}
	}
}

func TestFirmwareRoutesWithoutAStore(t *testing.T) {
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.RateLimit.Disabled = true })
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	if resp, _ := rawReq(t, srv, "GET", "/v1/firmware", testToken, nil, nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("list without store = %d, want 503", resp.StatusCode)
	}
}

func TestDoctorFirmwareCheck(t *testing.T) {
	k := newOTAKnob(t)
	got := checkFirmware(k.app)
	if got.Status != StatusOK || !strings.Contains(got.Detail, "images=0") {
		t.Fatalf("empty = %+v", got)
	}
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	got = checkFirmware(k.app)
	if got.Status != StatusOK || !strings.Contains(got.Detail, "images=1") || !strings.Contains(got.Detail, "bytes=") {
		t.Fatalf("one image = %+v", got)
	}
	if got := checkFirmware(newTestApp(t)); got.Status != StatusOK || !strings.Contains(got.Detail, "no data dir") {
		t.Fatalf("no store = %+v", got)
	}
}

func TestDoctorWarnsWhenAKnobSkippedTheUSBStep(t *testing.T) {
	k := newOTAKnob(t)
	k.checkin(t, otaReport("0.9.14", runningBuild, "new", "idle", true, ""))
	got := checkDevices(k.app)
	if got.Status != StatusWarn || !strings.Contains(got.Detail, "rollback bootloader not active") {
		t.Fatalf("devices = %+v", got)
	}
}
