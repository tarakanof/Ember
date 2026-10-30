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

	"github.com/tarakanof/ember/internal/devices"
	"github.com/tarakanof/ember/internal/nowplaying"
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
	if code != http.StatusAccepted || res.Status != "queued" || res.Target != "M4" {
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
	if len(got) != 1 || got[0].Gesture != gesturePush || got[0].Steps != 0 || got[0].ID == "" {
		t.Fatalf("M4 got %+v", got)
	}
	if again := pollControls(t, srv, "M4", 0); len(again) != 0 {
		t.Fatalf("delivered twice: %+v", again)
	}
}

func pairControlsKnob(t *testing.T, app *App, srv *httptest.Server, pages []string, pageOn bool) mintResp {
	t.Helper()
	knob := mintKnob(t, srv, http.StatusCreated)
	if pageOn {
		resp, b := devReq(t, srv, "PUT", "/v1/devices/"+knob.ID+"/config", testToken, `{"pages":[{"id":"bot","on":true},{"id":"controls","on":true}]}`)
		mustOK(t, "page on", resp, b)
	}
	app.devices.WithState(func(st *devices.State) {
		st.FindKnob(knob.ID).Caps = &devices.Caps{View: []int{1, 1}, Pages: pages, Features: []string{}}
	})
	return knob
}

func TestControlsActionAcceptsTheKnobDeviceToken(t *testing.T) {
	app, srv := controlsServer(t, "M4")
	knob := pairControlsKnob(t, app, srv, []string{"bot", "controls"}, true)
	pollControls(t, srv, "M4", 0)
	resp, b := npDo(t, srv, "POST", "/v1/controls/action", knob.Token, []byte(`{"gesture":"turn","steps":3}`),
		map[string]string{"Idempotency-Key": "t1"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("device token: %d %s", resp.StatusCode, b)
	}
	assertDeviceGolden(t, "controls_action", b)
	got := pollControls(t, srv, "M4", 0)
	if len(got) != 1 || got[0].Gesture != gestureTurn || got[0].Steps != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestControlsActionNeedsTheControlsPageOnAndInCaps(t *testing.T) {
	cases := map[string]struct {
		pages  []string
		pageOn bool
	}{
		"page off":       {[]string{"bot", "controls"}, false},
		"caps lack it":   {[]string{"bot"}, false},
		"on but no caps": {[]string{"bot"}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			app, srv := controlsServer(t, "M4")
			knob := pairControlsKnob(t, app, srv, c.pages, c.pageOn)
			pollControls(t, srv, "M4", 0)
			code, _, b := controlsAction(t, srv, knob.Token, `{"gesture":"push"}`, "k1")
			if code != http.StatusForbidden || !strings.Contains(b, `"error":"controls page is off on this knob"`) {
				t.Fatalf("got %d %s, want 403 controls page is off", code, b)
			}
			if got := pollControls(t, srv, "M4", 0); len(got) != 0 {
				t.Fatalf("queued %+v", got)
			}
		})
	}
}

func TestControlsActionRefusalKeepsTheKeyAndTheRateBudget(t *testing.T) {
	app, srv := controlsServer(t, "M4")
	knob := pairControlsKnob(t, app, srv, []string{"bot", "controls"}, false)
	pollControls(t, srv, "M4", 0)
	for range nowplaying.ControlBurst + 5 {
		if code, _, _ := controlsAction(t, srv, knob.Token, `{"gesture":"push"}`, "same"); code != http.StatusForbidden {
			t.Fatalf("got %d, want 403 every time", code)
		}
	}
	resp, b := devReq(t, srv, "PUT", "/v1/devices/"+knob.ID+"/config", testToken, `{"pages":[{"id":"bot","on":true},{"id":"controls","on":true}]}`)
	mustOK(t, "page on", resp, b)
	if code, res, b := controlsAction(t, srv, knob.Token, `{"gesture":"push"}`, "same"); code != http.StatusAccepted || res.Status != "queued" {
		t.Fatalf("after turning the page on: %d %s", code, b)
	}
}

func TestControlsActionOwnerTokenIgnoresThePageGate(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	pollControls(t, srv, "M4", 0)
	if code, _, b := controlsAction(t, srv, testToken, `{"gesture":"push"}`, ""); code != http.StatusAccepted {
		t.Fatalf("owner: %d %s", code, b)
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

func TestControlsActionIsRateLimitedPerCaller(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	pollControls(t, srv, "M4", 0)
	limited := false
	for i := range nowplaying.ControlBurst + 5 {
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

func TestControlQueueDropsStaleStepsSoonerThanPresses(t *testing.T) {
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newControlQueue()
	q.clock = func() time.Time { return now }
	q.lastPoll["M4"] = now
	_, _ = q.push("M4", controlCommand{Gesture: gestureTurn, Steps: 2})
	_, _ = q.push("M4", controlCommand{Gesture: gestureLongPush})
	now = now.Add(controlStepTTL + time.Millisecond)
	got, _ := q.take(context.Background(), "M4", 0)
	if len(got) != 1 || got[0].Gesture != gestureLongPush || got[0].AgeMS != (controlStepTTL+time.Millisecond).Milliseconds() {
		t.Fatalf("after the step TTL: %+v", got)
	}
	_, _ = q.push("M4", controlCommand{Gesture: gesturePush})
	now = now.Add(controlPressTTL)
	if got, _ := q.take(context.Background(), "M4", 0); len(got) != 0 {
		t.Fatalf("after the press TTL: %+v", got)
	}
}

func TestControlQueueKeepsTheNewestEight(t *testing.T) {
	q := newControlQueue()
	q.lastPoll["M4"] = time.Now()
	for range controlsPerMac + 3 {
		_, _ = q.push("M4", controlCommand{Gesture: gesturePush})
	}
	got, _ := q.take(context.Background(), "M4", 0)
	if len(got) != controlsPerMac || got[0].ID != "k4" || got[len(got)-1].ID != "k"+strconv.Itoa(controlsPerMac+3) {
		t.Fatalf("got %+v", got)
	}
}

func TestControlQueueRefusesAMacThatStoppedListening(t *testing.T) {
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newControlQueue()
	q.clock = func() time.Time { return now }
	q.lastPoll["M4"] = now
	now = now.Add(controlListenGrace + time.Second)
	if _, err := q.push("M4", controlCommand{Gesture: gesturePush}); err != errControlTargetDeaf {
		t.Fatalf("err = %v", err)
	}
}

func TestControlsConfigPutIsMasterOnly(t *testing.T) {
	_, srv := controlsServer(t, "")
	admin := mintClient(t, srv, "admin", scopeAdmin)
	if code, _, b := putControls(t, srv, admin.Token, `{"target":"Elsewhere"}`); code != http.StatusForbidden {
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
	if code, _, b := putControls(t, srv, testToken, `{"target":"M4"}`); code != http.StatusOK {
		t.Fatalf("put: %d %s", code, b)
	}
	srv.Close()
	_ = app.store.Close()
	app2, _ := newDevicesApp(t, db)
	app2.settings.reapply()
	got := app2.settings.controls.get()
	if got.Target != "M4" {
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
	app, srv := controlsServer(t, "M4")
	knob := pairControlsKnob(t, app, srv, []string{"bot", "controls"}, true)
	ingest := mintClient(t, srv, "ingest", scopeIngest)
	admin := mintClient(t, srv, "admin", scopeAdmin)
	adminWant := map[string]int{
		"GET /v1/controls/commands?mac=M4": http.StatusForbidden,
		"GET /v1/controls/config":          http.StatusOK,
		"PUT /v1/controls/config":          http.StatusForbidden,
	}
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
		}{{"none", "", 401}, {"wrong", "wrong", 401}, {"knob", knob.Token, 401}, {"ingest client", ingest.Token, 403},
			{"admin client", admin.Token, adminWant[r.method+" "+r.path]}} {
			if resp, b := devReq(t, srv, r.method, r.path, c.token, r.body); resp.StatusCode != c.want {
				t.Errorf("%s %s as %s: %d %s, want %d", r.method, r.path, c.name, resp.StatusCode, b, c.want)
			}
		}
	}
	if got := pollControls(t, srv, "M4", 0); len(got) != 1 {
		t.Fatalf("a rejected poll drained the queue: %+v", got)
	}
}

func TestControlQueueCoalescesTurnsButNotPresses(t *testing.T) {
	q := newControlQueue()
	q.lastPoll["M4"] = time.Now()
	for _, c := range []controlCommand{
		{Gesture: gestureTurn, Steps: 4}, {Gesture: gestureTurn, Steps: 2}, {Gesture: gestureTurn, Steps: 98},
		{Gesture: gesturePush}, {Gesture: gesturePush}, {Gesture: gestureTurn, Steps: -1},
	} {
		if _, err := q.push("M4", c); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := q.take(context.Background(), "M4", 0)
	want := []controlCommand{{Gesture: gestureTurn, Steps: maxQueuedSteps}, {Gesture: gesturePush}, {Gesture: gesturePush},
		{Gesture: gestureTurn, Steps: -1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].Gesture != w.Gesture || got[i].Steps != w.Steps {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestControlsConfigHoldsOnlyTheTarget(t *testing.T) {
	_, srv := controlsServer(t, "")
	code, got, b := putControls(t, srv, testToken, `{"target":" M4 "}`)
	if code != http.StatusOK || got.Target != "M4" || got.Macs == nil {
		t.Fatalf("put: %d %s", code, b)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(b), &raw)
	if len(raw) != 2 {
		t.Fatalf("config carries more than target and macs: %s", b)
	}
	long := strings.Repeat("x", maxControlTarget+1)
	if code, _, b := putControls(t, srv, testToken, `{"target":"`+long+`"}`); code != http.StatusBadRequest {
		t.Fatalf("long target: %d %s", code, b)
	}
}

func TestControlsCommandsCarryOnlyTheGesture(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	pollControls(t, srv, "M4", 0)
	if code, _, b := controlsAction(t, srv, testToken, `{"gesture":"turn","steps":-2}`, ""); code != http.StatusAccepted {
		t.Fatalf("turn: %d %s", code, b)
	}
	resp, b := npDo(t, srv, "GET", "/v1/controls/commands?mac=M4", testToken, nil, nil)
	var raw struct {
		Commands []map[string]json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(b, &raw); err != nil || resp.StatusCode != http.StatusOK || len(raw.Commands) != 1 {
		t.Fatalf("poll: %d %s", resp.StatusCode, b)
	}
	for k := range raw.Commands[0] {
		switch k {
		case "id", "gesture", "steps", "age_ms":
		default:
			t.Errorf("command carries %q: %s", k, b)
		}
	}
}

func TestControlQueueDropsTurnsThatCancelOut(t *testing.T) {
	_, srv := controlsServer(t, "M4")
	pollControls(t, srv, "M4", 0)
	for _, body := range []string{`{"gesture":"turn","steps":1}`, `{"gesture":"turn","steps":-1}`, `{"gesture":"push"}`} {
		if code, _, b := controlsAction(t, srv, testToken, body, ""); code != http.StatusAccepted {
			t.Fatalf("%s: %d %s", body, code, b)
		}
	}
	got := pollControls(t, srv, "M4", 0)
	if len(got) != 1 || got[0].Gesture != gesturePush {
		t.Fatalf("got %+v, want only the push", got)
	}
}

func TestControlQueueEvictsQueuedCommandsWithTheirMac(t *testing.T) {
	q := newControlQueue()
	for i := range controlMacsMax * 2 {
		mac := "M" + strconv.Itoa(i)
		_, _ = q.take(context.Background(), mac, 0)
		if _, err := q.push(mac, controlCommand{Gesture: gesturePush}); err != nil {
			t.Fatal(err)
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.lastPoll) > controlMacsMax || len(q.queued) > controlMacsMax+1 {
		t.Fatalf("lastPoll %d, queued %d; want both bounded by %d", len(q.lastPoll), len(q.queued), controlMacsMax)
	}
	for m := range q.queued {
		if _, ok := q.lastPoll[m]; !ok {
			t.Errorf("queue for %s outlived its Mac", m)
		}
	}
}
