package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeDump(seed byte, n int) ([]byte, string) {
	body := make([]byte, n-4)
	for i := range body {
		body[i] = seed + byte(i*7)
	}
	sum := crc32.ChecksumIEEE(body)
	body = binary.LittleEndian.AppendUint32(body, sum)
	return body, fmt.Sprintf("%08x", sum)
}

func putDump(t *testing.T, srv *httptest.Server, token, id string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest("PUT", srv.URL+"/v1/devices/self/coredump?id="+id, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func crashCheckin(t *testing.T, srv *httptest.Server, token, id string) map[string]any {
	t.Helper()
	body := `{"fw":"0.9.14","diag":{"crash":{"elf":"a1b2c3d4","id":"` + id + `","size":4096,"pc":"0x4201a2b3","reason":"panic","task":"ember"}}}`
	resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", token, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mintSecondKnob(t *testing.T, srv *httptest.Server) mintResp {
	t.Helper()
	resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, `{"kind":"cinder-knob","hw_id":"aabbccddeeff","name":"Shelf knob"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint = %d: %s", resp.StatusCode, b)
	}
	var m mintResp
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func listDumps(t *testing.T, srv *httptest.Server, token, device string) (int, []coredumpMeta) {
	t.Helper()
	resp, b := devReq(t, srv, "GET", "/v1/devices/"+device+"/coredumps", token, "")
	var out []coredumpMeta
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("list body %s: %v", b, err)
		}
	}
	return resp.StatusCode, out
}

func TestDeviceCheckinRecordsTheCrashIDAndSize(t *testing.T) {
	app, post := newDiagKnob(t)
	post(`{"crash":{"id":"1a2b3c4d","size":131072,"reason":"panic"}}`)
	got := storedDiag(app)
	if got == nil || got.Crash == nil || got.Crash.ID != "1a2b3c4d" || got.Crash.Size != 131072 {
		t.Fatalf("diag = %+v", got)
	}
}

func TestDeviceCheckinDropsADiagWithAnInvalidCrashIDOrSize(t *testing.T) {
	for name, diag := range map[string]string{
		"id upper":   `{"crash":{"id":"1A2B3C4D","size":10}}`,
		"id short":   `{"crash":{"id":"1a2b3c4","size":10}}`,
		"id long":    `{"crash":{"id":"1a2b3c4d0","size":10}}`,
		"id not hex": `{"crash":{"id":"1a2b3c4g","size":10}}`,
		"size zero":  `{"crash":{"id":"1a2b3c4d","size":0}}`,
		"size big":   `{"crash":{"id":"1a2b3c4d","size":131073}}`,
		"size neg":   `{"crash":{"id":"1a2b3c4d","size":-1}}`,
		"size alone": `{"crash":{"size":10}}`,
		"id alone":   `{"crash":{"id":"1a2b3c4d"}}`,
		"elf upper":  `{"crash":{"elf":"A1B2C3D4"}}`,
		"elf short":  `{"crash":{"elf":"a1b2c3"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			app, post := newDiagKnob(t)
			post(diag)
			if got := app.devices.list()[0].LastCheckin; got.Diag != nil {
				t.Fatalf("diag kept: %+v", got.Diag.Crash)
			}
		})
	}
}

func TestCheckinAsksForACoredumpOnlyWhenTheCrashHasAnID(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	_, plain := checkin(t, srv, m.Token, 0)
	_, old := func() (*http.Response, map[string]any) {
		resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"diag":`+fullDiag+`}`)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return resp, out
	}()
	for name, res := range map[string]map[string]any{"no diag": plain, "crash without id": old} {
		if _, ok := res["coredump_wanted"]; ok {
			t.Errorf("%s: coredump_wanted in %v", name, res)
		}
		if _, ok := res["coredump_ack"]; ok {
			t.Errorf("%s: coredump_ack in %v", name, res)
		}
	}
	res := crashCheckin(t, srv, m.Token, "1a2b3c4d")
	if res["coredump_wanted"] != "1a2b3c4d" {
		t.Fatalf("checkin = %v, want coredump_wanted", res)
	}
	if _, ok := res["coredump_ack"]; ok {
		t.Fatalf("ack before upload: %v", res)
	}
}

func TestCoredumpUploadIsStoredAndAcked(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	body, id := fakeDump(1, 4096)
	crashCheckin(t, srv, m.Token, id)
	if resp, b := putDump(t, srv, m.Token, id, bytes.NewReader(body)); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("upload = %d: %s", resp.StatusCode, b)
	}
	res := crashCheckin(t, srv, m.Token, id)
	if res["coredump_ack"] != id {
		t.Fatalf("checkin = %v, want coredump_ack %s", res, id)
	}
	if _, ok := res["coredump_wanted"]; ok {
		t.Fatalf("wanted after upload: %v", res)
	}
	if resp, b := putDump(t, srv, m.Token, id, bytes.NewReader(body)); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("re-upload = %d: %s", resp.StatusCode, b)
	}
	_, list := listDumps(t, srv, testToken, m.ID)
	if len(list) != 1 {
		t.Fatalf("list after re-upload = %+v", list)
	}
}

func TestCoredumpIDsDifferForDifferentDumps(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	a, idA := fakeDump(1, 4096)
	b, idB := fakeDump(2, 4096)
	if idA == idB {
		t.Fatalf("two dumps share id %s", idA)
	}
	for id, body := range map[string][]byte{idA: a, idB: b} {
		if resp, out := putDump(t, srv, m.Token, id, bytes.NewReader(body)); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("upload %s = %d: %s", id, resp.StatusCode, out)
		}
	}
	if _, list := listDumps(t, srv, testToken, m.ID); len(list) != 2 {
		t.Fatalf("list = %+v, want two dumps", list)
	}
}

func TestCoredumpUploadRejectsABadBody(t *testing.T) {
	good, id := fakeDump(1, 4096)
	tail := bytes.Clone(good)
	tail[len(tail)-1] ^= 0xff
	payload := bytes.Clone(good)
	payload[10] ^= 0xff
	other, otherID := fakeDump(3, 4096)
	cases := map[string]struct {
		id   string
		body []byte
		want int
	}{
		"payload changed":        {id, payload, http.StatusBadRequest},
		"trailer mismatch":       {id, tail, http.StatusBadRequest},
		"id of another dump":     {otherID, good, http.StatusBadRequest},
		"crc of the whole image": {fmt.Sprintf("%08x", crc32.ChecksumIEEE(other)), other, http.StatusBadRequest},
		"too short":              {"00000000", []byte{0, 0, 0, 0}, http.StatusBadRequest},
		"empty":                  {"00000000", nil, http.StatusBadRequest},
		"id upper":               {strings.ToUpper(id), good, http.StatusBadRequest},
		"id missing":             {"", good, http.StatusBadRequest},
		"too large":              {"00000000", make([]byte, 131073), http.StatusRequestEntityTooLarge},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			app, srv := newDevicesApp(t, "")
			m := mintKnob(t, srv, http.StatusCreated)
			if resp, b := putDump(t, srv, m.Token, c.id, bytes.NewReader(c.body)); resp.StatusCode != c.want {
				t.Fatalf("upload = %d, want %d: %s", resp.StatusCode, c.want, b)
			}
			if _, list := listDumps(t, srv, testToken, m.ID); len(list) != 0 {
				t.Fatalf("stored after a rejected upload: %+v", list)
			}
			entries, _ := os.ReadDir(filepath.Join(app.coredumps.dir, m.ID))
			if len(entries) != 0 {
				t.Fatalf("files left behind: %v", entries)
			}
		})
	}
}

func TestCoredumpUploadAcceptsTheLargestDump(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	body, id := fakeDump(5, 131072)
	if resp, b := putDump(t, srv, m.Token, id, bytes.NewReader(body)); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("upload = %d: %s", resp.StatusCode, b)
	}
}

func TestCoredumpUploadAnswersConflictWhileOneRuns(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	body, id := fakeDump(1, 4096)
	pr, pw := io.Pipe()
	done := make(chan int, 1)
	go func() {
		resp, _ := putDump(t, srv, m.Token, id, pr)
		done <- resp.StatusCode
	}()
	if _, err := pw.Write(body[:100]); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !app.coredumps.uploading(m.ID) {
		if time.Now().After(deadline) {
			t.Fatal("first upload never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	other, otherID := fakeDump(2, 4096)
	if resp, b := putDump(t, srv, m.Token, otherID, bytes.NewReader(other)); resp.StatusCode != http.StatusConflict {
		t.Fatalf("second upload = %d, want 409: %s", resp.StatusCode, b)
	}
	if _, err := pw.Write(body[100:]); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	if got := recvWithin(t, done, "first upload"); got != http.StatusNoContent {
		t.Fatalf("first upload = %d", got)
	}
	if resp, b := putDump(t, srv, m.Token, otherID, bytes.NewReader(other)); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("upload after the first finished = %d: %s", resp.StatusCode, b)
	}
}

func TestCoredumpsKeepTheNewestThree(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	base := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	n := 0
	app.coredumps.now = func() time.Time { n++; return base.Add(time.Duration(n) * time.Minute) }
	var ids []string
	for i := range 4 {
		body, id := fakeDump(byte(i+1), 1024)
		if resp, b := putDump(t, srv, m.Token, id, bytes.NewReader(body)); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("upload %d = %d: %s", i, resp.StatusCode, b)
		}
		ids = append(ids, id)
	}
	_, list := listDumps(t, srv, testToken, m.ID)
	got := make([]string, 0, len(list))
	for _, d := range list {
		got = append(got, d.ID)
	}
	want := []string{ids[3], ids[2], ids[1]}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("list = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(app.coredumps.dir, m.ID, ids[0]+".bin")); !os.IsNotExist(err) {
		t.Fatalf("oldest dump still on disk: %v", err)
	}
	res := crashCheckin(t, srv, m.Token, ids[0])
	if res["coredump_wanted"] != ids[0] {
		t.Fatalf("pruned dump still acked: %v", res)
	}
}

func TestCoredumpOwnerListDownloadAndDelete(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	admin := mintClient(t, srv, "ops", "admin").Token
	body, id := fakeDump(9, 2048)
	crashCheckin(t, srv, m.Token, id)
	putDump(t, srv, m.Token, id, bytes.NewReader(body))

	resp, raw := devReq(t, srv, "GET", "/v1/devices/"+m.ID+"/coredumps", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d: %s", resp.StatusCode, raw)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %s", raw)
	}
	d := list[0]
	if d["id"] != id || d["size"] != float64(2048) || d["fw"] != "0.9.14" || d["reason"] != "panic" || d["task"] != "ember" || d["pc"] != "0x4201a2b3" || d["elf"] != "a1b2c3d4" {
		t.Fatalf("list entry = %v", d)
	}
	if _, err := time.Parse(time.RFC3339, d["received_at"].(string)); err != nil {
		t.Fatalf("received_at = %v", d["received_at"])
	}

	for _, tok := range []string{testToken, admin} {
		resp, got := devReq(t, srv, "GET", "/v1/devices/"+m.ID+"/coredumps/"+id, tok, "")
		if resp.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
			t.Fatalf("download = %d, %d bytes", resp.StatusCode, len(got))
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Fatalf("Content-Type = %q", ct)
		}
		want := `attachment; filename="` + m.ID + `-0.9.14-` + id + `.bin"`
		if cd := resp.Header.Get("Content-Disposition"); cd != want {
			t.Fatalf("Content-Disposition = %q, want %q", cd, want)
		}
		if cl := resp.Header.Get("Content-Length"); cl != strconv.Itoa(len(body)) {
			t.Fatalf("Content-Length = %q", cl)
		}
	}
	for path, want := range map[string]int{
		"/v1/devices/" + m.ID + "/coredumps/0badc0de": http.StatusNotFound,
		"/v1/devices/" + m.ID + "/coredumps/BADC0DE1": http.StatusNotFound,
		"/v1/devices/" + m.ID + "/coredumps/zzzzzzzz": http.StatusNotFound,
		"/v1/devices/knob-000000/coredumps/" + id:     http.StatusNotFound,
		"/v1/devices/knob-000000/coredumps":           http.StatusNotFound,
	} {
		if resp, b := devReq(t, srv, "GET", path, testToken, ""); resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d: %s", path, resp.StatusCode, want, b)
		}
	}
	if resp, b := devReq(t, srv, "DELETE", "/v1/devices/"+m.ID+"/coredumps/"+id, admin, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", resp.StatusCode, b)
	}
	if resp, _ := devReq(t, srv, "DELETE", "/v1/devices/"+m.ID+"/coredumps/"+id, testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", resp.StatusCode)
	}
	if _, list := listDumps(t, srv, testToken, m.ID); len(list) != 0 {
		t.Fatalf("list after delete = %+v", list)
	}
	if res := crashCheckin(t, srv, m.Token, id); res["coredump_wanted"] != id {
		t.Fatalf("deleted dump still acked: %v", res)
	}
}

func TestCoredumpListIsAnEmptyArray(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	resp, b := devReq(t, srv, "GET", "/v1/devices/"+m.ID+"/coredumps", testToken, "")
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("list = %d %s", resp.StatusCode, b)
	}
}

func TestDeviceDeleteRemovesItsCoredumps(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	body, id := fakeDump(1, 1024)
	putDump(t, srv, m.Token, id, bytes.NewReader(body))
	dir := filepath.Join(app.coredumps.dir, m.ID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	if resp, b := devReq(t, srv, "DELETE", "/v1/devices/"+m.ID, testToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", resp.StatusCode, b)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dump dir after device delete: %v", err)
	}
	m2 := mintKnob(t, srv, http.StatusCreated)
	if _, list := listDumps(t, srv, testToken, m2.ID); len(list) != 0 {
		t.Fatalf("re-minted knob inherited dumps: %+v", list)
	}
}

func TestDeviceTokenCannotReachAnotherDevicesCoredumps(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	a := mintKnob(t, srv, http.StatusCreated)
	b := mintSecondKnob(t, srv)
	bodyA, idA := fakeDump(1, 1024)
	bodyB, idB := fakeDump(2, 1024)
	putDump(t, srv, a.Token, idA, bytes.NewReader(bodyA))
	putDump(t, srv, b.Token, idB, bytes.NewReader(bodyB))

	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/devices/" + a.ID + "/coredumps"},
		{"GET", "/v1/devices/" + a.ID + "/coredumps/" + idA},
		{"DELETE", "/v1/devices/" + a.ID + "/coredumps/" + idA},
		{"GET", "/v1/devices/" + b.ID + "/coredumps"},
	} {
		if resp, out := devReq(t, srv, c.method, c.path, b.Token, ""); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s as knob B = %d, want 401: %s", c.method, c.path, resp.StatusCode, out)
		}
	}
	_, listA := listDumps(t, srv, testToken, a.ID)
	_, listB := listDumps(t, srv, testToken, b.ID)
	if len(listA) != 1 || listA[0].ID != idA || len(listB) != 1 || listB[0].ID != idB {
		t.Fatalf("lists = %+v / %+v", listA, listB)
	}
	if res := crashCheckin(t, srv, b.Token, idA); res["coredump_wanted"] != idA {
		t.Fatalf("knob B acked knob A's dump: %v", res)
	}
	if resp, _ := putDump(t, srv, testToken, idA, bytes.NewReader(bodyA)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("owner token on the device upload = %d, want 401", resp.StatusCode)
	}
}

func TestCoredumpFileIsTheExactUpload(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	body, id := fakeDump(4, 3000)
	putDump(t, srv, m.Token, id, bytes.NewReader(body))
	got, err := os.ReadFile(filepath.Join(app.coredumps.dir, m.ID, id+".bin"))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("stored file: %v, equal=%v", err, bytes.Equal(got, body))
	}
}

func TestCoredumpsLiveInTheDataDir(t *testing.T) {
	db := filepath.Join(t.TempDir(), "data", "pomodoro.db")
	app, _ := newDevicesApp(t, db)
	if want := filepath.Join(filepath.Dir(db), "coredumps"); app.coredumps == nil || app.coredumps.dir != want {
		t.Fatalf("coredump dir = %+v, want %s", app.coredumps, want)
	}
}

func TestCheckinWithoutCoredumpStorageAsksForNothing(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	app.coredumps = nil
	m := mintKnob(t, srv, http.StatusCreated)
	res := crashCheckin(t, srv, m.Token, "1a2b3c4d")
	if _, ok := res["coredump_wanted"]; ok {
		t.Fatalf("checkin = %v", res)
	}
	body, id := fakeDump(1, 64)
	if resp, _ := putDump(t, srv, m.Token, id, bytes.NewReader(body)); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upload without storage = %d, want 503", resp.StatusCode)
	}
}

func uploadDumps(t *testing.T, srv *httptest.Server, token string, seeds ...byte) []string {
	t.Helper()
	var ids []string
	for _, seed := range seeds {
		body, id := fakeDump(seed, 1024)
		if resp, b := putDump(t, srv, token, id, bytes.NewReader(body)); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("upload %s = %d: %s", id, resp.StatusCode, b)
		}
		ids = append(ids, id)
	}
	return ids
}

func dumpIDs(t *testing.T, srv *httptest.Server, device string) string {
	t.Helper()
	_, list := listDumps(t, srv, testToken, device)
	ids := make([]string, 0, len(list))
	for _, d := range list {
		ids = append(ids, d.ID)
	}
	return strings.Join(ids, ",")
}

func TestCoredumpPruningKeepsTheNewUploadWhenTheClockStepsBack(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	base := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	steps := []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour, 0}
	n := 0
	app.coredumps.now = func() time.Time { d := steps[min(n, len(steps)-1)]; n++; return base.Add(d) }
	ids := uploadDumps(t, srv, m.Token, 1, 2, 3, 4)
	if got, want := dumpIDs(t, srv, m.ID), strings.Join([]string{ids[3], ids[2], ids[1]}, ","); got != want {
		t.Fatalf("list = %s, want %s", got, want)
	}
	if res := crashCheckin(t, srv, m.Token, ids[3]); res["coredump_ack"] != ids[3] {
		t.Fatalf("checkin after the 4th upload = %v, want ack", res)
	}
}

func TestCoredumpPruningKeepsTheNewUploadInTheSameSecond(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	same := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	app.coredumps.now = func() time.Time { return same }
	ids := uploadDumps(t, srv, m.Token, 9, 8, 7, 6)
	if got, want := dumpIDs(t, srv, m.ID), strings.Join([]string{ids[3], ids[2], ids[1]}, ","); got != want {
		t.Fatalf("list = %s, want %s", got, want)
	}
	if res := crashCheckin(t, srv, m.Token, ids[3]); res["coredump_ack"] != ids[3] {
		t.Fatalf("checkin after the 4th upload = %v, want ack", res)
	}
}

func plantCoredumpLeftovers(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".0badc0de.bin.tmp-123", ".0badc0de.json.tmp-456", "0badc0de.bin", "deadbeef.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func dirNames(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func TestCoredumpInsertSweepsLeftoverFiles(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	dir := filepath.Join(app.coredumps.dir, m.ID)
	plantCoredumpLeftovers(t, dir)
	id := uploadDumps(t, srv, m.Token, 1)[0]
	if got, want := dirNames(t, dir), id+".bin,"+id+".json"; got != want {
		t.Fatalf("files = %s, want %s", got, want)
	}
}

func TestCoredumpStartupSweepsLeftoverFiles(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app1, srv1 := newDevicesApp(t, db)
	m := mintKnob(t, srv1, http.StatusCreated)
	id := uploadDumps(t, srv1, m.Token, 1)[0]
	dir := filepath.Join(app1.coredumps.dir, m.ID)
	plantCoredumpLeftovers(t, dir)
	other := filepath.Join(app1.coredumps.dir, "knob-aabbcc")
	plantCoredumpLeftovers(t, other)

	newDevicesApp(t, db)
	if got, want := dirNames(t, dir), id+".bin,"+id+".json"; got != want {
		t.Fatalf("files after restart = %s, want %s", got, want)
	}
	if got := dirNames(t, other); got != "" {
		t.Fatalf("other device files after restart = %s", got)
	}
}
