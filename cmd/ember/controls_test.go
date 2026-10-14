package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func controlsAction(t *testing.T, srv *httptest.Server, token, body, key string) (int, controlsActionAnswer, string) {
	t.Helper()
	var h map[string]string
	if key != "" {
		h = map[string]string{"Idempotency-Key": key}
	}
	resp, b := npDo(t, srv, "POST", "/v1/controls/action", token, []byte(body), h)
	var res controlsActionAnswer
	_ = json.Unmarshal(b, &res)
	return resp.StatusCode, res, string(b)
}

func pollControls(t *testing.T, srv *httptest.Server, mac string, wait int) []controlCommand {
	t.Helper()
	resp, b := npDo(t, srv, "GET", "/v1/controls/commands?mac="+mac+"&wait="+strconv.Itoa(wait), testToken, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("controls commands: %d %s", resp.StatusCode, b)
	}
	var a controlCommandsAnswer
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	return a.Commands
}

func putControls(t *testing.T, srv *httptest.Server, token, body string) (int, controlsConfigAnswer, string) {
	t.Helper()
	resp, b := devReq(t, srv, "PUT", "/v1/controls/config", token, body)
	var a controlsConfigAnswer
	_ = json.Unmarshal(b, &a)
	return resp.StatusCode, a, string(b)
}

func controlsServer(t *testing.T, target string) (*App, *httptest.Server) {
	t.Helper()
	app, srv := newDevicesApp(t, "")
	if target != "" {
		if code, _, b := putControls(t, srv, testToken, `{"target":"`+target+`"}`); code != http.StatusOK {
			t.Fatalf("set target: %d %s", code, b)
		}
	}
	return app, srv
}

func TestControlsActionRejectsMissingOrForeignTokens(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	admin := mintClient(t, srv, "admin", scopeAdmin)
	for name, token := range map[string]string{"none": "", "wrong": "wrong", "admin client": admin.Token} {
		if code, _, b := controlsAction(t, srv, token, `{"gesture":"push"}`, ""); code != http.StatusUnauthorized {
			t.Errorf("%s: %d %s, want 401", name, code, b)
		}
	}
}

func TestControlsActionRejectsInvalidGestures(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	for _, body := range []string{`{"gesture":"spin"}`, `{"gesture":"turn"}`, `{"gesture":"turn","steps":21}`,
		`{"gesture":"push","steps":1}`, `{"gesture":"push","script":"do shell script \"x\""}`, `not json`} {
		if code, _, b := controlsAction(t, srv, testToken, body, ""); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", body, code, b)
		}
	}
}

func TestControlsActionWithoutTargetIs503(t *testing.T) {
	_, srv := controlsServer(t, "")
	if code, _, b := controlsAction(t, srv, testToken, `{"gesture":"push"}`, ""); code != http.StatusServiceUnavailable {
		t.Fatalf("no target: %d %s", code, b)
	}
}

func TestControlsActionNeedsTheTargetToListen(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	if code, _, b := controlsAction(t, srv, testToken, `{"gesture":"push"}`, ""); code != http.StatusServiceUnavailable {
		t.Fatalf("target never polled: %d %s", code, b)
	}
	pollControls(t, srv, "M5", 0)
	if code, _, b := controlsAction(t, srv, testToken, `{"gesture":"push"}`, ""); code != http.StatusServiceUnavailable {
		t.Fatalf("only another Mac polled: %d %s", code, b)
	}
	pollControls(t, srv, "M4", 0)
	code, res, b := controlsAction(t, srv, testToken, `{"gesture":"push"}`, "")
	if code != http.StatusAccepted || res.Status != "queued" || res.Kind != controlMedia || res.Target != "M4" {
		t.Fatalf("after the target polled: %d %s", code, b)
	}
}

func TestControlsCommandsReachOnlyTheTargetMac(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	pollControls(t, srv, "M4", 0)
	pollControls(t, srv, "M5", 0)
	if code, _, b := controlsAction(t, srv, testToken, `{"gesture":"push"}`, ""); code != http.StatusAccepted {
		t.Fatalf("push: %d %s", code, b)
	}
	if got := pollControls(t, srv, "M5", 0); len(got) != 0 {
		t.Fatalf("M5 got %+v", got)
	}
	got := pollControls(t, srv, "M4", 0)
	if len(got) != 1 || got[0].Kind != controlMedia || got[0].Key != mediaKeyPlayPause || got[0].ID == "" {
		t.Fatalf("M4 got %+v", got)
	}
	if again := pollControls(t, srv, "M4", 0); len(again) != 0 {
		t.Fatalf("delivered twice: %+v", again)
	}
}

func TestControlsActionAcceptsTheKnobDeviceToken(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	knob := mintKnob(t, srv, http.StatusCreated)
	pollControls(t, srv, "M4", 0)
	resp, b := npDo(t, srv, "POST", "/v1/controls/action", knob.Token, []byte(`{"gesture":"turn","steps":3}`),
		map[string]string{"Idempotency-Key": "t1"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("device token: %d %s", resp.StatusCode, b)
	}
	assertDeviceGolden(t, "controls_action", b)
	got := pollControls(t, srv, "M4", 0)
	if len(got) != 1 || got[0].Kind != controlVolume || got[0].Delta != 3*defaultVolumeStep {
		t.Fatalf("got %+v", got)
	}
}

func TestControlsIdempotencyKeyDedupesAndFailuresReleaseIt(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	if code, _, _ := controlsAction(t, srv, testToken, `{"gesture":"push"}`, "k1"); code != http.StatusServiceUnavailable {
		t.Fatalf("not listening: %d", code)
	}
	pollControls(t, srv, "M4", 0)
	if code, _, b := controlsAction(t, srv, testToken, `{"gesture":"push"}`, "k1"); code != http.StatusAccepted {
		t.Fatalf("retry after a 503: %d %s", code, b)
	}
	if code, res, b := controlsAction(t, srv, testToken, `{"gesture":"push"}`, "k1"); code != http.StatusOK || res.Status != "duplicate" {
		t.Fatalf("repeat: %d %s", code, b)
	}
	if got := pollControls(t, srv, "M4", 0); len(got) != 1 {
		t.Fatalf("got %d commands, want 1: %+v", len(got), got)
	}
	resp, _ := npDo(t, srv, "POST", "/v1/controls/action", testToken, []byte(`{"gesture":"push"}`),
		map[string]string{"Idempotency-Key": strings.Repeat("k", maxControlKey+1)})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("long key: %d", resp.StatusCode)
	}
}

func TestControlsUnassignedGestureQueuesNothing(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	pollControls(t, srv, "M4", 0)
	if code, res, b := controlsAction(t, srv, testToken, `{"gesture":"double_push"}`, ""); code != http.StatusOK || res.Status != "unassigned" {
		t.Fatalf("double_push: %d %s", code, b)
	}
	if got := pollControls(t, srv, "M4", 0); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestControlsActionIsRateLimitedPerCaller(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	pollControls(t, srv, "M4", 0)
	limited := false
	for i := range controlBurst + 5 {
		code, _, _ := controlsAction(t, srv, testToken, `{"gesture":"push"}`, "r"+strconv.Itoa(i))
		if code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("never limited")
	}
}

func TestControlsResolveTurnAgainstTheConfiguredAction(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	if code, _, b := putControls(t, srv, testToken, `{"turn":{"kind":"scroll","lines":4,"axis":"horizontal"}}`); code != http.StatusOK {
		t.Fatalf("put: %d %s", code, b)
	}
	pollControls(t, srv, "M4", 0)
	if code, _, b := controlsAction(t, srv, testToken, `{"gesture":"turn","steps":-2}`, ""); code != http.StatusAccepted {
		t.Fatalf("turn: %d %s", code, b)
	}
	got := pollControls(t, srv, "M4", 0)
	if len(got) != 1 || got[0].Kind != controlScroll || got[0].Axis != scrollAxisHorizontal || got[0].Delta != -8 {
		t.Fatalf("got %+v", got)
	}
}

func TestControlsLongPollWakesOnAction(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	pollControls(t, srv, "M4", 0)
	done := make(chan []controlCommand, 1)
	go func() { done <- pollControls(t, srv, "M4", 10) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, _, _ := controlsAction(t, srv, testToken, `{"gesture":"push"}`, "")
		if code == http.StatusAccepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("push: %d", code)
		}
	}
	select {
	case got := <-done:
		if len(got) == 0 {
			t.Fatal("woke with no commands")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not wake")
	}
}

func TestControlsCommandsNeedAMac(t *testing.T) {
	_, srv := controlsServer(t, "")
	for _, q := range []string{"", "?mac=", "?mac=M4&wait=x"} {
		if resp, b := npDo(t, srv, "GET", "/v1/controls/commands"+q, testToken, nil, nil); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: %d %s", q, resp.StatusCode, b)
		}
	}
}

func TestControlQueueCoalescesVolumeAndSameAxisScroll(t *testing.T) {
	q := newControlQueue()
	q.lastPoll["M4"] = time.Now()
	for _, c := range []controlCommand{
		{Kind: controlVolume, Delta: 4}, {Kind: controlVolume, Delta: 2}, {Kind: controlVolume, Delta: 98},
		{Kind: controlScroll, Axis: scrollAxisVertical, Delta: 3}, {Kind: controlScroll, Axis: scrollAxisVertical, Delta: 3},
		{Kind: controlScroll, Axis: scrollAxisHorizontal, Delta: 1},
		{Kind: controlMedia, Key: mediaKeyNext}, {Kind: controlMedia, Key: mediaKeyNext},
	} {
		if _, err := q.push("M4", c); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := q.take(context.Background(), "M4", 0)
	want := []struct {
		kind  controlKind
		delta int
	}{{controlVolume, 100}, {controlScroll, 6}, {controlScroll, 1}, {controlMedia, 0}, {controlMedia, 0}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].Delta != w.delta {
			t.Errorf("[%d] = %+v, want %v %d", i, got[i], w.kind, w.delta)
		}
	}
}

func TestControlQueueDropsStaleStepsSoonerThanPresses(t *testing.T) {
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newControlQueue()
	q.clock = func() time.Time { return now }
	q.lastPoll["M4"] = now
	_, _ = q.push("M4", controlCommand{Kind: controlVolume, Delta: 2})
	_, _ = q.push("M4", controlCommand{Kind: controlShortcut, Name: "Focus"})
	now = now.Add(controlStepTTL + time.Millisecond)
	got, _ := q.take(context.Background(), "M4", 0)
	if len(got) != 1 || got[0].Kind != controlShortcut || got[0].AgeMS != (controlStepTTL+time.Millisecond).Milliseconds() {
		t.Fatalf("after the step TTL: %+v", got)
	}
	_, _ = q.push("M4", controlCommand{Kind: controlMute})
	now = now.Add(controlPressTTL)
	if got, _ := q.take(context.Background(), "M4", 0); len(got) != 0 {
		t.Fatalf("after the press TTL: %+v", got)
	}
}

func TestControlQueueKeepsTheNewestEight(t *testing.T) {
	q := newControlQueue()
	q.lastPoll["M4"] = time.Now()
	for i := range controlsPerMac + 3 {
		_, _ = q.push("M4", controlCommand{Kind: controlShortcut, Name: strconv.Itoa(i)})
	}
	got, _ := q.take(context.Background(), "M4", 0)
	if len(got) != controlsPerMac || got[0].Name != "3" || got[len(got)-1].Name != strconv.Itoa(controlsPerMac+2) {
		t.Fatalf("got %+v", got)
	}
}

func TestControlQueueRefusesAMacThatStoppedListening(t *testing.T) {
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newControlQueue()
	q.clock = func() time.Time { return now }
	q.lastPoll["M4"] = now
	now = now.Add(controlListenGrace + time.Second)
	if _, err := q.push("M4", controlCommand{Kind: controlMute}); err != errControlTargetDeaf {
		t.Fatalf("err = %v", err)
	}
}

func TestControlsConfigDefaults(t *testing.T) {
	_, srv := controlsServer(t, "")
	resp, b := devReq(t, srv, "GET", "/v1/controls/config", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d %s", resp.StatusCode, b)
	}
	var got controlsConfigAnswer
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Target != "" || got.Push != (ControlAction{Kind: controlMedia, Key: mediaKeyPlayPause}) ||
		got.DoublePush.Kind != controlNone || got.LongPush.Kind != controlNone ||
		got.Turn != (ControlAction{Kind: controlVolume, Step: defaultVolumeStep}) || got.Macs == nil {
		t.Fatalf("defaults: %s", b)
	}
}

func TestControlsConfigRejectsInvalidActions(t *testing.T) {
	_, srv := controlsServer(t, "")
	long := make([]byte, maxAppleScriptBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	for _, body := range []string{
		`{"push":{"kind":"volume"}}`,
		`{"turn":{"kind":"applescript","script":"beep"}}`,
		`{"turn":{"kind":"shortcut","name":"x"}}`,
		`{"push":{"kind":"media","key":"stop"}}`,
		`{"push":{"kind":"shortcut","name":"  "}}`,
		`{"long_push":{"kind":"applescript","script":" "}}`,
		`{"long_push":{"kind":"applescript","script":"` + string(long) + `"}}`,
		`{"turn":{"kind":"volume","step":21}}`,
		`{"turn":{"kind":"scroll","lines":11}}`,
		`{"turn":{"kind":"scroll","axis":"diagonal"}}`,
		`{"push":{"kind":"launch"}}`,
		`{"target":"` + string(long[:maxControlTarget+1]) + `"}`,
	} {
		if code, _, b := putControls(t, srv, testToken, body); code != http.StatusBadRequest {
			t.Errorf("%.60s: %d %s, want 400", body, code, b)
		}
	}
}

func TestControlsConfigNormalizesAndKeepsOtherGestures(t *testing.T) {
	_, srv := controlsServer(t, "")
	code, got, b := putControls(t, srv, testToken,
		`{"target":" M4 ","long_push":{"kind":"shortcut","name":" Focus ","script":"ignored","step":3},"turn":{"kind":"scroll"}}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, b)
	}
	if got.Target != "M4" || got.LongPush != (ControlAction{Kind: controlShortcut, Name: "Focus"}) ||
		got.Turn != (ControlAction{Kind: controlScroll, Lines: defaultScrollLines, Axis: scrollAxisVertical}) ||
		got.Push.Kind != controlMedia {
		t.Fatalf("put answer: %s", b)
	}
	code, got, b = putControls(t, srv, testToken, `{"double_push":{"kind":"applescript","script":"beep"}}`)
	if code != http.StatusOK || got.Target != "M4" || got.LongPush.Name != "Focus" || got.DoublePush.Script != "beep" {
		t.Fatalf("merge: %d %s", code, b)
	}
}

func TestControlsConfigPutIsMasterOnly(t *testing.T) {
	_, srv := controlsServer(t, "")
	admin := mintClient(t, srv, "admin", scopeAdmin)
	if code, _, b := putControls(t, srv, admin.Token, `{"long_push":{"kind":"applescript","script":"beep"}}`); code != http.StatusForbidden {
		t.Fatalf("admin client: %d %s", code, b)
	}
	if resp, b := devReq(t, srv, "GET", "/v1/controls/config", admin.Token, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin client get: %d %s", resp.StatusCode, b)
	}
}

func TestControlsConfigListsMacsSeenPolling(t *testing.T) {
	_, srv := controlsServer(t, "")
	pollControls(t, srv, "M5", 0)
	pollControls(t, srv, "M4", 0)
	resp, b := devReq(t, srv, "GET", "/v1/controls/config", testToken, "")
	var got controlsConfigAnswer
	if err := json.Unmarshal(b, &got); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d %s", resp.StatusCode, b)
	}
	if len(got.Macs) != 2 || got.Macs[0].Name != "M4" || !got.Macs[0].Listening || got.Macs[0].LastPoll == "" ||
		got.Macs[1].Name != "M5" {
		t.Fatalf("macs: %s", b)
	}
}

func TestControlsConfigSurvivesARestart(t *testing.T) {
	db := t.TempDir() + "/s.db"
	app, srv := newDevicesApp(t, db)
	if code, _, b := putControls(t, srv, testToken, `{"target":"M4","long_push":{"kind":"shortcut","name":"Focus"}}`); code != http.StatusOK {
		t.Fatalf("put: %d %s", code, b)
	}
	srv.Close()
	_ = app.store.Close()
	app2, _ := newDevicesApp(t, db)
	app2.settings.reapply()
	got := app2.settings.controls.get()
	if got.Target != "M4" || got.LongPush.Name != "Focus" {
		t.Fatalf("after restart: %+v", got)
	}
}

func TestControlsActionRejectsAnUnpairedKnob(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	knob := mintKnob(t, srv, http.StatusCreated)
	pollControls(t, srv, "M4", 0)
	if resp, b := devReq(t, srv, "DELETE", "/v1/devices/"+knob.ID, testToken, ""); resp.StatusCode/100 != 2 {
		t.Fatalf("delete knob: %d %s", resp.StatusCode, b)
	}
	if code, _, b := controlsAction(t, srv, knob.Token, `{"gesture":"push"}`, ""); code != http.StatusUnauthorized {
		t.Fatalf("deleted knob: %d %s, want 401", code, b)
	}
}

func TestControlsOwnerRoutesRejectKnobAndNarrowClientTokens(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	knob := mintKnob(t, srv, http.StatusCreated)
	ingest := mintClient(t, srv, "ingest", scopeIngest)
	pollControls(t, srv, "M4", 0)
	if code, _, b := controlsAction(t, srv, knob.Token, `{"gesture":"push"}`, ""); code != http.StatusAccepted {
		t.Fatalf("queue one: %d %s", code, b)
	}
	routes := []struct{ method, path, body string }{
		{"GET", "/v1/controls/commands?mac=M4", ""},
		{"GET", "/v1/controls/config", ""},
		{"PUT", "/v1/controls/config", `{"target":"M5"}`},
	}
	for _, r := range routes {
		for _, c := range []struct {
			name, token string
			want        int
		}{{"none", "", 401}, {"wrong", "wrong", 401}, {"knob", knob.Token, 401}, {"ingest client", ingest.Token, 403}} {
			if resp, b := devReq(t, srv, r.method, r.path, c.token, r.body); resp.StatusCode != c.want {
				t.Errorf("%s %s as %s: %d %s, want %d", r.method, r.path, c.name, resp.StatusCode, b, c.want)
			}
		}
	}
	if got := pollControls(t, srv, "M4", 0); len(got) != 1 {
		t.Fatalf("a rejected poll drained the queue: %+v", got)
	}
}
