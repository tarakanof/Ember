package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func shownFixture(t *testing.T) *viewFixture {
	t.Helper()
	f := newViewFixture(t)
	f.app.viewClock = f.clk.Now
	caps := maps.Clone(knobCapsFull)
	caps["features"] = []string{featureViewWait, featureShown}
	postCapsCheckin(t, f, "0.10.0", caps)
	return f
}

func pollShown(t *testing.T, f *viewFixture, shown string) {
	t.Helper()
	resp, b := f.getQuery(t, "?shown="+url.QueryEscape(shown), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("view with shown=%q = %d: %s", shown, resp.StatusCode, b)
	}
}

func listedDevice(t *testing.T, f *viewFixture) map[string]json.RawMessage {
	t.Helper()
	resp, b := devReq(t, f.srv, "GET", "/v1/devices", testToken, "")
	mustOK(t, "device list", resp, b)
	var out struct {
		Devices []map[string]json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	for _, d := range out.Devices {
		if string(d["id"]) == `"`+f.m.ID+`"` {
			return d
		}
	}
	t.Fatalf("device %s not listed: %s", f.m.ID, b)
	return nil
}

func listedShown(t *testing.T, f *viewFixture) string {
	t.Helper()
	return string(listedDevice(t, f)["shown"])
}

func TestKnobShownPollReachesTheDeviceList(t *testing.T) {
	f := shownFixture(t)
	if got := listedShown(t, f); got != "" {
		t.Fatalf("shown before any report = %s, want absent", got)
	}
	pollShown(t, f, "weather:wind")
	if got, want := listedShown(t, f), `{"page":"weather","face":"wind","at":"2026-06-21T12:00:00Z"}`; got != want {
		t.Fatalf("shown = %s, want %s", got, want)
	}
	f.clk.advance(3 * time.Second)
	pollShown(t, f, "nowplaying")
	if got, want := listedShown(t, f), `{"page":"nowplaying","face":null,"at":"2026-06-21T12:00:03Z"}`; got != want {
		t.Fatalf("shown = %s, want %s", got, want)
	}
}

func TestKnobShownRenameReplyCarriesIt(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "bot")
	resp, b := devReq(t, f.srv, "PATCH", "/v1/devices/"+f.m.ID, testToken, `{"name":"Desk"}`)
	mustOK(t, "rename", resp, b)
	if !strings.Contains(string(b), `"shown":{"page":"bot","face":null,`) {
		t.Fatalf("rename reply = %s, want shown", b)
	}
}

func TestKnobShownGoesStaleFiftySecondsAfterTheLastPoll(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "weather:rain")
	f.clk.advance(40 * time.Second)
	pollShown(t, f, "weather:rain")
	f.clk.advance(knobShownFresh)
	if got := listedShown(t, f); !strings.Contains(got, `"at":"2026-06-21T12:00:40Z"`) {
		t.Fatalf("shown 50 s after the last poll = %s, want it kept with the refreshed at", got)
	}
	f.clk.advance(time.Second)
	if got := listedShown(t, f); got != "" {
		t.Fatalf("shown 51 s after the last poll = %s, want absent", got)
	}
}

func TestKnobShownIsIgnoredWithoutTheShownCap(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		f := newViewFixture(t)
		pollShown(t, f, "weather:wind")
		if got := listedShown(t, f); got != "" {
			t.Fatalf("legacy knob shown = %s, want absent", got)
		}
	})
	t.Run("caps without shown", func(t *testing.T) {
		f := newViewFixture(t)
		postCapsCheckin(t, f, "0.10.0", knobCapsFull)
		pollShown(t, f, "weather:wind")
		if got := listedShown(t, f); got != "" {
			t.Fatalf("caps knob without shown = %s, want absent", got)
		}
	})
}

func TestKnobShownMalformedValuesAreIgnoredAndLoggedOnce(t *testing.T) {
	f := shownFixture(t)
	var logs syncBuffer
	f.app.logger = slog.New(slog.NewTextHandler(&logs, nil))
	pollShown(t, f, "weather:temp")
	want := listedShown(t, f)
	bad := []string{"", "Weather", "weather:", "weather:wind:x", "weather:Wind", "weather:windyday1",
		"1bot", "now-playing", ":wind", strings.Repeat("a", 25), "weather:abcdefghi", "bot\n"}
	for _, v := range bad {
		pollShown(t, f, v)
		if got := listedShown(t, f); got != want {
			t.Fatalf("after shown=%q: %s, want the last good %s", v, got, want)
		}
	}
	if n := strings.Count(logs.String(), "knob shown ignored"); n != 1 {
		t.Fatalf("malformed logs = %d, want 1:\n%s", n, logs.String())
	}
	pollShown(t, f, "pomodoro")
	pollShown(t, f, "Bad")
	if n := strings.Count(logs.String(), "knob shown ignored"); n != 2 {
		t.Fatalf("malformed logs after a good value = %d, want 2", n)
	}
}

func TestKnobShownLongestValidValuesAreKept(t *testing.T) {
	f := shownFixture(t)
	page := "a" + strings.Repeat("b_9", 7) + "zz"
	pollShown(t, f, page+":abcdefgh")
	if got, want := listedShown(t, f), `{"page":"`+page+`","face":"abcdefgh","at":"2026-06-21T12:00:00Z"}`; got != want {
		t.Fatalf("shown = %s, want %s", got, want)
	}
}

func TestKnobShownIsRecordedBeforeTheLongPollWaits(t *testing.T) {
	f := shownFixture(t)
	resp, _ := f.get(t, "")
	etag := resp.Header.Get("ETag")
	waiting := make(chan struct{})
	f.app.viewWaitHook = func() {
		select {
		case waiting <- struct{}{}:
		default:
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, err := http.NewRequestWithContext(ctx, "GET", f.srv.URL+knobViewPath+"?wait=25&shown=weather:air", nil)
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+f.m.Token)
		req.Header.Set("If-None-Match", etag)
		if r, err := f.srv.Client().Do(req); err == nil {
			_ = r.Body.Close()
		}
	}()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("long poll never waited")
	}
	got := listedShown(t, f)
	cancel()
	<-done
	if !strings.Contains(got, `"page":"weather","face":"air"`) {
		t.Fatalf("shown while the long poll waits = %s, want weather:air", got)
	}
}

func TestKnobShownIsForgottenWhenTheDeviceIsDeleted(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "bot")
	resp, b := devReq(t, f.srv, "DELETE", "/v1/devices/"+f.m.ID, testToken, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", resp.StatusCode, b)
	}
	m := mintKnob(t, f.srv, http.StatusCreated)
	if m.ID != f.m.ID {
		t.Fatalf("re-minted id %s, want the same %s", m.ID, f.m.ID)
	}
	f.m = m
	if got := listedShown(t, f); got != "" {
		t.Fatalf("shown after delete and re-mint = %s, want absent", got)
	}
}

func TestKnobViewPollWithShownGolden(t *testing.T) {
	f := shownFixture(t)
	type pollRequest struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Query  struct {
			Wait  string `json:"wait"`
			Shown string `json:"shown"`
		} `json:"query"`
	}
	want := pollRequest{Method: "GET", Path: knobViewPath}
	want.Query.Wait = "25"
	want.Query.Shown = "weather:wind"
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	assertDeviceGolden(t, "view_req_shown", body)
	raw, err := os.ReadFile(filepath.Join("testdata", "devices", "view_req_shown.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Method string            `json:"method"`
		Path   string            `json:"path"`
		Query  map[string]string `json:"query"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	q := url.Values{}
	for k, v := range fixture.Query {
		q.Set(k, v)
	}
	req, err := http.NewRequest(fixture.Method, f.srv.URL+fixture.Path+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.m.Token)
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fixture poll = %d", resp.StatusCode)
	}
	page, face, _ := strings.Cut(fixture.Query["shown"], ":")
	if got := listedShown(t, f); !strings.Contains(got, `"page":"`+page+`","face":"`+face+`"`) {
		t.Fatalf("shown after the fixture's poll = %s, want %s", got, fixture.Query["shown"])
	}
}

func TestKnobShownAtIsWholeSecondsUTC(t *testing.T) {
	f := shownFixture(t)
	f.clk.mu.Lock()
	f.clk.now = time.Date(2026, 6, 21, 14, 0, 7, 987654321, time.FixedZone("CEST", 2*3600))
	f.clk.mu.Unlock()
	pollShown(t, f, "bot")
	if got, want := listedShown(t, f), `{"page":"bot","face":null,"at":"2026-06-21T12:00:07Z"}`; got != want {
		t.Fatalf("shown = %s, want %s", got, want)
	}
}

func TestKnobShownDisappearsWhenCapsWithdrawTheToken(t *testing.T) {
	invalid := maps.Clone(knobCapsFull)
	invalid["features"] = []string{"np-control", featureShown}
	without := maps.Clone(knobCapsFull)
	cases := []struct {
		name string
		caps any
	}{
		{"caps without shown", without},
		{"no caps", nil},
		{"invalid caps", invalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := shownFixture(t)
			pollShown(t, f, "weather:wind")
			if got := listedShown(t, f); got == "" {
				t.Fatal("shown missing before the caps change")
			}
			postCapsCheckin(t, f, "0.10.0", c.caps)
			if got := listedShown(t, f); got != "" {
				t.Fatalf("list shown after the caps change = %s, want absent", got)
			}
			resp, b := devReq(t, f.srv, "PATCH", "/v1/devices/"+f.m.ID, testToken, `{"name":"Desk"}`)
			mustOK(t, "rename", resp, b)
			if strings.Contains(string(b), `"shown"`) {
				t.Fatalf("rename reply after the caps change = %s, want no shown", b)
			}
		})
	}
}

func TestKnobShownPollRacingADeleteLeavesNothingForTheReMintedKnob(t *testing.T) {
	f := shownFixture(t)
	deleted := make(chan int, 1)
	f.app.shownSetHook = func() {
		f.app.shownSetHook = nil
		go func() {
			resp, _ := devReq(t, f.srv, "DELETE", "/v1/devices/"+f.m.ID, testToken, "")
			deleted <- resp.StatusCode
		}()
		select {
		case code := <-deleted:
			deleted <- code
		case <-time.After(200 * time.Millisecond):
		}
	}
	pollShown(t, f, "weather:wind")
	if code := <-deleted; code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	m := mintKnob(t, f.srv, http.StatusCreated)
	if m.ID != f.m.ID {
		t.Fatalf("re-minted id %s, want the same %s", m.ID, f.m.ID)
	}
	f.m = m
	caps := maps.Clone(knobCapsFull)
	caps["features"] = []string{featureShown}
	postCapsCheckin(t, f, "0.10.0", caps)
	if got := listedShown(t, f); got != "" {
		t.Fatalf("re-minted knob shown = %s, want absent", got)
	}
}
