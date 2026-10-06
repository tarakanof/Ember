package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

const runningBuild = "77aa01ff"

type otaKnob struct {
	app  *App
	srv  *httptest.Server
	knob mintResp
}

func newOTAKnob(t *testing.T) otaKnob {
	t.Helper()
	app, srv := newDevicesApp(t, "")
	return otaKnob{app: app, srv: srv, knob: mintKnob(t, srv, http.StatusCreated)}
}

func rawReq(t *testing.T, srv *httptest.Server, method, path, token string, body []byte, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func (k otaKnob) upload(t *testing.T, img []byte, query string) firmwareImage {
	t.Helper()
	resp, b := rawReq(t, k.srv, "POST", "/v1/firmware"+query, testToken, img, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d: %s", resp.StatusCode, b)
	}
	var out firmwareImage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func otaReport(fw, build, image, phase string, rollback bool, last string) string {
	ota := fmt.Sprintf(`{"image":%q,"phase":%q,"rollback":%t,"slot":0`, image, phase, rollback)
	if last != "" {
		ota += `,"last":` + last
	}
	return fmt.Sprintf(`{"fw":%q,"fw_build":%q,"config_version":1,"ota":%s}}`, fw, build, ota)
}

func (k otaKnob) checkin(t *testing.T, body string) map[string]any {
	t.Helper()
	resp, b := devReq(t, k.srv, "POST", "/v1/devices/self/checkin", k.knob.Token, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (k otaKnob) idle(t *testing.T) map[string]any {
	t.Helper()
	return k.checkin(t, otaReport("0.9.13", runningBuild, "valid", "idle", true, ""))
}

func (k otaKnob) put(t *testing.T, body string) (int, otaStatus) {
	t.Helper()
	resp, b := devReq(t, k.srv, "PUT", "/v1/devices/"+k.knob.ID+"/ota", testToken, body)
	var st otaStatus
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &st); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, st
}

func (k otaKnob) status(t *testing.T) otaStatus {
	t.Helper()
	resp, b := devReq(t, k.srv, "GET", "/v1/devices/"+k.knob.ID+"/ota", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ota status = %d: %s", resp.StatusCode, b)
	}
	var st otaStatus
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func (k otaKnob) target(t *testing.T, version string) {
	t.Helper()
	if code, _ := k.put(t, `{"target":"`+version+`"}`); code != http.StatusOK {
		t.Fatalf("set target = %d", code)
	}
}

func (k otaKnob) download(t *testing.T, version string, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	return rawReq(t, k.srv, "GET", "/v1/devices/self/firmware/"+version, k.knob.Token, nil, header)
}

func offerOf(t *testing.T, reply map[string]any) map[string]any {
	t.Helper()
	o, _ := reply["ota"].(map[string]any)
	return o
}

func TestOTAOfferCarriesTheExactContract(t *testing.T) {
	k := newOTAKnob(t)
	img := fakeFirmware(fwOpts{version: "0.9.14"})
	stored := k.upload(t, img, "")
	k.idle(t)
	k.target(t, "0.9.14")
	resp, b := devReq(t, k.srv, "POST", "/v1/devices/self/checkin", k.knob.Token, otaReport("0.9.13", runningBuild, "valid", "idle", true, ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d", resp.StatusCode)
	}
	sum := sha256.Sum256(img)
	want := fmt.Sprintf(`"ota":{"auto":false,"build":%q,"retry":false,"sha256":%q,"size":%d,"url":"/v1/devices/self/firmware/0.9.14","version":"0.9.14"}`,
		stored.Build, hex.EncodeToString(sum[:]), len(img))
	if !strings.Contains(string(b), want) {
		t.Fatalf("reply %s\nwant %s", b, want)
	}
	if st := k.status(t); st.Phase != otaPhaseOffered || st.From == nil || *st.From != "0.9.13" {
		t.Fatalf("status = %+v", st)
	}
}

func TestOTAOfferGating(t *testing.T) {
	cases := []struct {
		name    string
		report  string
		setup   func(t *testing.T, k otaKnob)
		offer   bool
		waiting string
	}{
		{name: "ready", report: otaReport("0.9.13", runningBuild, "valid", "idle", true, ""), offer: true},
		{name: "no rollback bootloader", report: otaReport("0.9.13", runningBuild, "valid", "idle", false, "")},
		{name: "no ota object", report: `{"fw":"0.9.13","fw_build":"77aa01ff"}`},
		{name: "pending verify", report: otaReport("0.9.13", runningBuild, "pending_verify", "idle", true, "")},
		{name: "image new", report: otaReport("0.9.13", runningBuild, "new", "idle", true, "")},
		{name: "knob waiting", report: otaReport("0.9.13", runningBuild, "valid", "waiting", true, "")},
		{name: "knob rebooting", report: otaReport("0.9.13", runningBuild, "valid", "rebooting", true, "")},
		{name: "pomodoro running", report: otaReport("0.9.13", runningBuild, "valid", "idle", true, ""),
			setup: func(t *testing.T, k otaKnob) { startPomodoro(t, k.app).Start(pomodoro.PhaseFocus) }, waiting: otaWaitPomodoro},
		{name: "pomodoro paused", report: otaReport("0.9.13", runningBuild, "valid", "idle", true, ""),
			setup: func(t *testing.T, k otaKnob) {
				e := startPomodoro(t, k.app)
				e.Start(pomodoro.PhaseFocus)
				e.Pause(time.Now())
			}, waiting: otaWaitPomodoro},
		{name: "pomodoro parked", report: otaReport("0.9.13", runningBuild, "valid", "idle", true, ""),
			setup: func(t *testing.T, k otaKnob) {
				e := startPomodoro(t, k.app)
				e.Start(pomodoro.PhaseFocus)
				e.Tick(time.Now().Add(26 * time.Minute))
				if st := e.Status(time.Now()); st.Running || st.Phase == pomodoro.PhaseIdle {
					t.Fatalf("not parked: %+v", st)
				}
			}, offer: true},
		{name: "coredump wanted", report: `{"fw":"0.9.13","fw_build":"77aa01ff","diag":{"crash":{"id":"1a2b3c4d","size":4096}},"ota":{"image":"valid","phase":"idle","rollback":true,"slot":0}}`,
			waiting: otaWaitCoredump},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newOTAKnob(t)
			k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
			k.idle(t)
			k.target(t, "0.9.14")
			if c.setup != nil {
				c.setup(t, k)
			}
			got := offerOf(t, k.checkin(t, c.report))
			if (got != nil) != c.offer {
				t.Fatalf("offer = %v, want offer %v", got, c.offer)
			}
			st := k.status(t)
			if w := st.WaitingFor; (w == nil && c.waiting != "") || (w != nil && *w != c.waiting) {
				t.Fatalf("waiting_for = %v, want %q", w, c.waiting)
			}
		})
	}
}

func startPomodoro(t *testing.T, app *App) *pomodoro.Engine {
	t.Helper()
	e := pomodoro.New(pomodoro.Settings{FocusMin: 25, ShortMin: 5, LongMin: 15, RoundsBeforeLong: 4}, realClock{})
	app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = true })
	app.EnablePomodoro(e, app.store)
	return e
}

func TestOTANoOfferWhenTheKnobRunsTheTargetBuild(t *testing.T) {
	k := newOTAKnob(t)
	img := k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.idle(t)
	k.target(t, "0.9.14")
	if got := offerOf(t, k.checkin(t, otaReport("0.9.14", img.Build, "valid", "idle", true, ""))); got != nil {
		t.Fatalf("offer = %v", got)
	}
	if st := k.status(t); st.Target != nil {
		t.Fatalf("target kept: %+v", st)
	}
}

func TestOTAAutoOffersOnlyNewerUnblockedReleases(t *testing.T) {
	cases := []struct {
		name    string
		version string
		channel string
		blocked bool
		offer   bool
	}{
		{"newer release", "0.9.14", "release", false, true},
		{"newer test", "0.9.14", "test", false, false},
		{"older release", "0.9.12", "release", false, false},
		{"same release", "0.9.13", "release", false, false},
		{"blocked release", "0.9.14", "release", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newOTAKnob(t)
			k.upload(t, fakeFirmware(fwOpts{version: c.version}), "?channel="+c.channel)
			k.idle(t)
			if code, _ := k.put(t, `{"mode":"auto"}`); code != http.StatusOK {
				t.Fatalf("mode = %d", code)
			}
			if c.blocked {
				_, _, err := k.app.devices.updateOTA(k.knob.ID, func(o *knobOTA, _ *deviceCheckin) (bool, error) {
					o.Blocked = []string{c.version}
					return false, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			got := offerOf(t, k.idle(t))
			if (got != nil) != c.offer {
				t.Fatalf("offer = %v, want %v", got, c.offer)
			}
			if got != nil && (got["auto"] != true || got["version"] != c.version) {
				t.Fatalf("offer = %v", got)
			}
			if st := k.status(t); c.offer && (st.WaitingFor == nil || *st.WaitingFor != otaWaitIdleInput) {
				t.Fatalf("waiting_for = %v, want idle_input", st.WaitingFor)
			}
		})
	}
}

func TestOTAManualOffersADowngrade(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.12"}), "")
	k.idle(t)
	k.target(t, "0.9.12")
	if got := offerOf(t, k.idle(t)); got == nil || got["version"] != "0.9.12" {
		t.Fatalf("offer = %v", got)
	}
}

func TestOTASetTargetBumpsTheEpoch(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.idle(t)
	before := k.app.devices.epochValue()
	k.target(t, "0.9.14")
	if after := k.app.devices.epochValue(); after <= before {
		t.Fatalf("epoch %d -> %d", before, after)
	}
	mid := k.app.devices.epochValue()
	k.idle(t)
	if k.app.devices.epochValue() != mid {
		t.Fatal("a checkin moved the epoch")
	}
}

func TestOTAPutValidation(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	if code, _ := k.put(t, `{"target":"0.9.14"}`); code != http.StatusConflict {
		t.Fatalf("no checkin yet = %d, want 409", code)
	}
	k.checkin(t, otaReport("0.9.13", runningBuild, "valid", "idle", false, ""))
	resp, b := devReq(t, k.srv, "PUT", "/v1/devices/"+k.knob.ID+"/ota", testToken, `{"target":"0.9.14"}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(b), `"no_rollback_bootloader"`) {
		t.Fatalf("old bootloader = %d %s", resp.StatusCode, b)
	}
	if code, st := k.put(t, `{"mode":"auto"}`); code != http.StatusOK || st.Mode != otaModeAuto {
		t.Fatalf("mode without rollback = %d %+v", code, st)
	}
	k.idle(t)
	for body, want := range map[string]int{
		`{"target":"0.9.99"}`:  http.StatusBadRequest,
		`{"target":14}`:        http.StatusBadRequest,
		`{"mode":"sometimes"}`: http.StatusBadRequest,
		`{"bogus":1}`:          http.StatusBadRequest,
		`{"retry":true}`:       http.StatusBadRequest,
		`{"target":"0.9.14"}`:  http.StatusOK,
		`{"target":null}`:      http.StatusOK,
	} {
		if code, _ := k.put(t, body); code != want {
			t.Errorf("%s = %d, want %d", body, code, want)
		}
	}
	resp, _ = devReq(t, k.srv, "PUT", "/v1/devices/knob-000000/ota", testToken, `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown device = %d", resp.StatusCode)
	}
}

func TestOTADownloadServesOnlyTheOfferedImage(t *testing.T) {
	k := newOTAKnob(t)
	img := fakeFirmware(fwOpts{version: "0.9.14"})
	k.upload(t, img, "")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.15", seed: 3}), "")
	k.idle(t)
	if resp, _ := k.download(t, "0.9.14", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("before offer = %d, want 409", resp.StatusCode)
	}
	if resp, _ := k.download(t, "0.9.99", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown version = %d, want 404", resp.StatusCode)
	}
	k.target(t, "0.9.14")
	k.idle(t)
	if resp, _ := k.download(t, "0.9.15", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("other stored version = %d, want 409", resp.StatusCode)
	}
	resp, b := k.download(t, "0.9.14", nil)
	sum := sha256.Sum256(img)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(b, img) {
		t.Fatalf("download = %d, %d bytes", resp.StatusCode, len(b))
	}
	for h, want := range map[string]string{
		"Content-Type":   "application/octet-stream",
		"Content-Length": fmt.Sprint(len(img)),
		"ETag":           `"` + hex.EncodeToString(sum[:]) + `"`,
		"Cache-Control":  "no-store",
		"Accept-Ranges":  "bytes",
	} {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	resp, _ = rawReq(t, k.srv, "GET", "/v1/devices/self/firmware/0.9.14", testToken, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("master token = %d, want 401", resp.StatusCode)
	}
}

func TestOTADownloadResumesWithRange(t *testing.T) {
	k := newOTAKnob(t)
	img := fakeFirmware(fwOpts{version: "0.9.14"})
	stored := k.upload(t, img, "")
	k.idle(t)
	k.target(t, "0.9.14")
	k.idle(t)
	etag := `"` + stored.SHA256 + `"`
	resp, b := k.download(t, "0.9.14", map[string]string{"Range": "bytes=40000-", "If-Range": etag})
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(b, img[40000:]) {
		t.Fatalf("resume = %d, %d bytes", resp.StatusCode, len(b))
	}
	if got := resp.Header.Get("Content-Range"); got != fmt.Sprintf("bytes 40000-%d/%d", len(img)-1, len(img)) {
		t.Fatalf("Content-Range = %q", got)
	}
	resp, b = k.download(t, "0.9.14", map[string]string{"Range": "bytes=40000-", "If-Range": `"stale"`})
	if resp.StatusCode != http.StatusOK || len(b) != len(img) {
		t.Fatalf("stale If-Range = %d, %d bytes", resp.StatusCode, len(b))
	}
}

func TestOTAProgressCountsBytesSent(t *testing.T) {
	k := newOTAKnob(t)
	img := fakeFirmware(fwOpts{version: "0.9.14"})
	k.upload(t, img, "")
	k.idle(t)
	k.target(t, "0.9.14")
	k.idle(t)
	if _, b := k.download(t, "0.9.14", map[string]string{"Range": "bytes=0-9999"}); len(b) != 10000 {
		t.Fatalf("partial = %d bytes", len(b))
	}
	st := k.status(t)
	if st.Phase != otaPhaseDownloading || st.Bytes == nil || *st.Bytes != 10000 || st.Size == nil || *st.Size != len(img) ||
		st.ProgressPct == nil || *st.ProgressPct != 10000*100/len(img) || st.StartedAt == nil {
		t.Fatalf("status = %+v", st)
	}
	k.download(t, "0.9.14", map[string]string{"Range": "bytes=10000-"})
	st = k.status(t)
	if *st.Bytes != int64(len(img)) || *st.ProgressPct != 100 || st.Phase != otaPhaseRestarting {
		t.Fatalf("after resume = %+v", st)
	}
}

func TestOTAPhasesFollowTheKnob(t *testing.T) {
	k := newOTAKnob(t)
	img := k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.idle(t)
	k.target(t, "0.9.14")
	k.idle(t)
	k.download(t, "0.9.14", nil)
	steps := []struct {
		report string
		phase  string
	}{
		{otaReport("0.9.13", runningBuild, "valid", "waiting", true, ""), otaPhaseInstalling},
		{otaReport("0.9.13", runningBuild, "valid", "rebooting", true, ""), otaPhaseRestarting},
		{otaReport("0.9.14", img.Build, "pending_verify", "idle", true, ""), otaPhaseVerifying},
		{otaReport("0.9.14", img.Build, "valid", "idle", true, `{"result":"ok","version":"0.9.14"}`), otaPhaseDone},
	}
	for _, s := range steps {
		if got := offerOf(t, k.checkin(t, s.report)); got != nil {
			t.Fatalf("%s: offer %v", s.phase, got)
		}
		if st := k.status(t); st.Phase != s.phase {
			t.Fatalf("phase = %s, want %s", st.Phase, s.phase)
		}
	}
	st := k.status(t)
	if st.Target != nil || st.FinishedAt == nil || st.Running == nil || st.Running.Build == nil || *st.Running.Build != img.Build {
		t.Fatalf("done status = %+v", st)
	}
}

func TestOTAFailureAndRollbackAreRecordedAndBlockAuto(t *testing.T) {
	for _, c := range []struct{ last, phase, errCode string }{
		{`{"error":"sha256","result":"failed","version":"0.9.14"}`, otaPhaseFailed, "sha256"},
		{`{"error":"no_checkin","result":"rolled_back","version":"0.9.14"}`, otaPhaseRolledBack, "no_checkin"},
	} {
		t.Run(c.phase, func(t *testing.T) {
			k := newOTAKnob(t)
			k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
			k.idle(t)
			k.target(t, "0.9.14")
			k.idle(t)
			k.download(t, "0.9.14", nil)
			got := offerOf(t, k.checkin(t, otaReport("0.9.13", runningBuild, "valid", "idle", true, c.last)))
			if got != nil {
				t.Fatalf("offered again after %s: %v", c.phase, got)
			}
			st := k.status(t)
			if st.Phase != c.phase || st.Error == nil || *st.Error != c.errCode || len(st.Blocked) != 1 || st.Blocked[0] != "0.9.14" {
				t.Fatalf("status = %+v", st)
			}
			code, st := k.put(t, `{"retry":true}`)
			if code != http.StatusOK || st.Phase != otaPhaseIdle || len(st.Blocked) != 0 {
				t.Fatalf("retry = %d %+v", code, st)
			}
			offer := offerOf(t, k.idle(t))
			if offer == nil || offer["retry"] != true {
				t.Fatalf("retry offer = %v", offer)
			}
			if offer := offerOf(t, k.idle(t)); offer == nil || offer["retry"] != false {
				t.Fatalf("second offer = %v, want retry false", offer)
			}
		})
	}
}

func TestOTAIgnoresAStaleLastResultBeforeTheDownload(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.idle(t)
	k.target(t, "0.9.14")
	stale := `{"error":"sha256","result":"failed","version":"0.9.14"}`
	if got := offerOf(t, k.checkin(t, otaReport("0.9.13", runningBuild, "valid", "idle", true, stale))); got == nil {
		t.Fatal("no offer")
	}
	k.checkin(t, otaReport("0.9.13", runningBuild, "valid", "idle", true, stale))
	if st := k.status(t); st.Phase != otaPhaseOffered {
		t.Fatalf("phase = %s, want offered", st.Phase)
	}
}

func TestOTAStatusShape(t *testing.T) {
	k := newOTAKnob(t)
	resp, b := devReq(t, k.srv, "GET", "/v1/devices/"+k.knob.ID+"/ota", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	want := `{"mode":"manual","target":null,"phase":"idle","progress_pct":null,"bytes":null,"size":null,"from":null,"error":null,"started_at":null,"finished_at":null,"blocked":[],"running":null,"available":null,"waiting_for":null}`
	if strings.TrimSpace(string(b)) != want {
		t.Fatalf("body %s\nwant %s", b, want)
	}
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.idle(t)
	st := k.status(t)
	if st.Available == nil || *st.Available != "0.9.14" || st.Running == nil || st.Running.FW != "0.9.13" ||
		st.Running.Slot == nil || *st.Running.Slot != 0 || st.Running.Image == nil || *st.Running.Image != "valid" || !st.Running.Rollback {
		t.Fatalf("status = %+v", st)
	}
}

func TestOTAInvalidReportIsDroppedButTheCheckinKept(t *testing.T) {
	k := newOTAKnob(t)
	k.checkin(t, `{"fw":"0.9.13","fw_build":"XYZ","ota":{"image":"weird","rollback":true}}`)
	c := k.app.devices.list()[0].LastCheckin
	if c == nil || c.FW != "0.9.13" || c.OTA != nil || c.FWBuild != "" {
		t.Fatalf("checkin = %+v", c)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	return nil
}

func TestOTADownloadRaisesTheWriteDeadline(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.idle(t)
	k.target(t, "0.9.14")
	k.idle(t)
	req := httptest.NewRequest("GET", "/v1/devices/self/firmware/0.9.14", nil)
	req.Header.Set("Authorization", "Bearer "+k.knob.Token)
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	k.app.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if left := time.Until(rec.deadline); left < 9*time.Minute {
		t.Fatalf("write deadline in %s, want about 10 min", left)
	}
}

func TestOTADeleteKnobForgetsProgress(t *testing.T) {
	k := newOTAKnob(t)
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.idle(t)
	k.target(t, "0.9.14")
	k.idle(t)
	k.download(t, "0.9.14", nil)
	if resp, _ := devReq(t, k.srv, "DELETE", "/v1/devices/"+k.knob.ID, testToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if _, ok := k.app.ota.get(k.knob.ID); ok {
		t.Fatal("progress kept")
	}
}

func TestKnobOTAGolden(t *testing.T) {
	k := newOTAKnob(t)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	k.app.devices.now = func() time.Time { return now }
	k.app.knobFW.now = func() time.Time { return now }
	get := func(path string) json.RawMessage {
		t.Helper()
		resp, b := devReq(t, k.srv, "GET", path, testToken, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
		return json.RawMessage(b)
	}
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "?channel=release")
	k.upload(t, fakeFirmware(fwOpts{version: "0.9.15-rc1", seed: 2}), "")
	k.idle(t)
	assertGolden(t, "knob_ota_idle", get("/v1/devices/"+k.knob.ID+"/ota"))
	assertGolden(t, "firmware_list", get("/v1/firmware"))
	k.target(t, "0.9.14")
	k.idle(t)
	k.download(t, "0.9.14", map[string]string{"Range": "bytes=0-29999"})
	assertGolden(t, "knob_ota_downloading", get("/v1/devices/"+k.knob.ID+"/ota"))
	k.download(t, "0.9.14", map[string]string{"Range": "bytes=30000-"})
	k.checkin(t, otaReport("0.9.13", runningBuild, "valid", "idle", true, `{"error":"no_checkin","result":"rolled_back","version":"0.9.14"}`))
	assertGolden(t, "knob_ota_rolled_back", get("/v1/devices/"+k.knob.ID+"/ota"))
}
