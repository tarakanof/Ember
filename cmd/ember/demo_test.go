package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/devices"
)

var knobCapsDemo = &devices.Caps{
	View:     []int{1, 1},
	Pages:    []string{"bot", "pomodoro", "weather", "nowplaying"},
	Features: []string{featureDemo, featureNPCover, devices.FeatureViewWait, featureWxFaces, featureWxMoments},
	Limits:   &devices.CapsLimits{ViewBytes: 16383, ConfigBytes: 2048},
}

func demoFixture(t *testing.T) *viewFixture {
	t.Helper()
	f := goldenViewFixture(t)
	f.app.viewClock = f.clk.Now
	f.app.devices.Now = f.clk.Now
	f.app.viewRecheck = time.Hour
	plantCaps(t, f, knobCapsDemo.Clone())
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":true}]}`)
	n := 0
	f.app.demos.newID = func() string { n++; return fmt.Sprintf("d%07d", n) }
	return f
}

func postDemo(t *testing.T, f *viewFixture, body string) (*http.Response, demoStatus) {
	t.Helper()
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/demo", testToken, body)
	var st demoStatus
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &st); err != nil {
			t.Fatal(err)
		}
	}
	return resp, st
}

func demoStatusOf(t *testing.T, f *viewFixture) demoStatus {
	t.Helper()
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/"+f.m.ID+"/demo", testToken, "")
	mustOK(t, "demo status", resp, b)
	var st demoStatus
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func viewDemo(t *testing.T, body []byte) *knobDemoWire {
	t.Helper()
	var v struct {
		Demo *knobDemoWire `json:"demo"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v.Demo
}

type knobDemoWire struct {
	ID      string                     `json:"id"`
	Page    string                     `json:"page"`
	View    map[string]json.RawMessage `json:"view"`
	Act     string                     `json:"act"`
	Offline bool                       `json:"offline"`
	Hold    *bool                      `json:"hold"`
}

func (f *viewFixture) report(t *testing.T, id, state string) (*http.Response, []byte) {
	t.Helper()
	return f.getQuery(t, "?demo_id="+id+"&demo_state="+state, "")
}

func (f *viewFixture) getQuery(t *testing.T, query, etag string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest("GET", f.srv.URL+knobViewPath+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.m.Token)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := f.srv.Client().Do(req)
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

func TestDemoCatalogHoldsTheIssueScenes(t *testing.T) {
	want := map[string]int{demoGroupBot: 14, demoGroupMoments: 18, demoGroupFaces: 78, demoGroupPomo: 7, demoGroupNP: 5}
	got := map[string]int{}
	var groups []string
	seen := map[string]bool{}
	for _, s := range demoCatalog {
		if seen[s.Name] {
			t.Errorf("scene %q listed twice", s.Name)
		}
		seen[s.Name] = true
		if s.Title == "" {
			t.Errorf("scene %q has no title", s.Name)
		}
		got[s.Group]++
		if len(groups) == 0 || groups[len(groups)-1] != s.Group {
			groups = append(groups, s.Group)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("scenes per group = %v, want %v", got, want)
	}
	if !slices.Equal(groups, []string{demoGroupBot, demoGroupMoments, demoGroupFaces, demoGroupPomo, demoGroupNP}) {
		t.Errorf("groups are not contiguous in order: %v", groups)
	}
	for _, name := range []string{"bot.sleepy", "bot.asleep", "bot.chase_half", "bot.offline", "bot.quiet_calm", "moment.afterrain.quiet",
		"face.air.fog", "face.days.dry", "sun.polar_night", "pomodoro.parked", "pomodoro.done", "nowplaying.nothing", "nowplaying.playing_light"} {
		if !seen[name] {
			t.Errorf("scene %q missing", name)
		}
	}
}

var (
	demoPagePattern = regexp.MustCompile(`^(bot|pomodoro|nowplaying|weather|weather:(temp|rain|cast|wind|days|sun|air))$`)
	demoActPattern  = regexp.MustCompile(`^(moment:(glance|night|frost|heat|wind|afterrain|snow|rain|storm):(full|quiet)|chase:(full|half)|hop|pop|blink|sleepy|asleep)$`)
)

func TestEveryDemoSceneYieldsAValidViewAndMatchesItsGolden(t *testing.T) {
	f := demoFixture(t)
	f.app.demos.newID = func() string { return "d0c0ffee" }
	names := map[string]bool{}
	for _, sc := range demoCatalog {
		names[sc.Name+".json"] = true
		t.Run(sc.Name, func(t *testing.T) {
			f.app.demos.start(f.m.ID, []string{sc.Name}, false, f.clk.Now())
			body := goldenView(t, f)
			d := viewDemo(t, body)
			if d == nil || d.ID != "d0c0ffee" || d.Hold == nil {
				t.Fatalf("demo block = %+v", d)
			}
			if !demoPagePattern.MatchString(d.Page) {
				t.Errorf("page %q outside the contract", d.Page)
			}
			if d.Act != "" && !demoActPattern.MatchString(d.Act) {
				t.Errorf("act %q outside the contract", d.Act)
			}
			if d.View == nil {
				t.Fatal("scene has no view")
			}
			if _, nested := d.View["demo"]; nested {
				t.Error("the demo view carries its own demo")
			}
			for _, k := range []string{"v", "epoch", "config_version", "mood", "brightness"} {
				if _, ok := d.View[k]; !ok {
					t.Errorf("view lacks %q", k)
				}
			}
			switch {
			case strings.HasPrefix(d.Page, "weather") || strings.HasPrefix(d.Act, "moment:"):
				var w map[string]json.RawMessage
				if err := json.Unmarshal(d.View["weather"], &w); err != nil || w["moon"] == nil || w["rain"] == nil {
					t.Errorf("weather view lacks the detail: %s", d.View["weather"])
				}
			case d.Page == "pomodoro":
				if string(d.View["pomo"]) == "" || string(d.View["pomo"]) == "null" {
					t.Error("pomodoro view lacks pomo")
				}
			case d.Page == "nowplaying":
				if d.View["nowplaying"] == nil {
					t.Error("now-playing view lacks nowplaying")
				}
			}
			if len(body) > knobCapsDemo.Limits.ViewBytes/2 {
				t.Errorf("view is %d B, over half the buffer", len(body))
			}
			assertDeviceGolden(t, filepath.Join("demo", sc.Name), body)
		})
	}
	if *updateGolden {
		return
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "devices", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !names[e.Name()] {
			t.Errorf("testdata/devices/demo/%s belongs to no scene", e.Name())
		}
	}
	if len(entries) != len(names) {
		t.Errorf("%d fixtures for %d scenes", len(entries), len(names))
	}
}

func TestDemoFixturesHoldNoSecrets(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "devices", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join("testdata", "devices", "demo", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"ekd_", "ekc_", testToken} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s contains %q", e.Name(), bad)
			}
		}
	}
}

func TestDemoReachesOnlyKnobsReportingTheDemoFeature(t *testing.T) {
	f := demoFixture(t)
	f.app.demos.start(f.m.ID, []string{"bot.idle"}, false, f.clk.Now())
	if viewDemo(t, goldenView(t, f)) == nil {
		t.Fatal("demo knob got no demo")
	}
	caps := knobCapsDemo.Clone()
	caps.Features = slices.DeleteFunc(caps.Features, func(s string) bool { return s == featureDemo })
	plantCaps(t, f, caps)
	if body := goldenView(t, f); strings.Contains(string(body), `"demo"`) {
		t.Fatalf("knob without the token got %s", body)
	}
	plantCaps(t, f, nil)
	if body := goldenView(t, f); strings.Contains(string(body), `"demo"`) {
		t.Fatalf("legacy knob got %s", body)
	}
	resp, _ := postDemo(t, f, `{"scene":"bot.idle"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("POST to a knob without the token = %d, want 409", resp.StatusCode)
	}
}

func TestDemoPostSetsAndNewestReplaces(t *testing.T) {
	f := demoFixture(t)
	resp, st := postDemo(t, f, `{"scene":"bot.working","hold":true}`)
	if resp.StatusCode != http.StatusOK || st != (demoStatus{State: demoPending, Scene: "bot.working", ID: "d0000001"}) {
		t.Fatalf("POST = %d %+v", resp.StatusCode, st)
	}
	d := viewDemo(t, goldenView(t, f))
	if d == nil || d.ID != "d0000001" || d.Page != "bot" || d.Hold == nil || !*d.Hold {
		t.Fatalf("demo = %+v", d)
	}
	_, st = postDemo(t, f, `{"scenes":["pomodoro.focus","pomodoro.break"],"hold":true}`)
	if st != (demoStatus{State: demoPending, Scene: "pomodoro.focus", ID: "d0000002", QueueLeft: 1}) {
		t.Fatalf("replacing POST = %+v", st)
	}
	d = viewDemo(t, goldenView(t, f))
	if d.ID != "d0000002" || d.Page != "pomodoro" || *d.Hold {
		t.Fatalf("demo after replace = %+v (hold is ignored with scenes)", d)
	}
}

func TestDemoPostRejectsBadBodies(t *testing.T) {
	f := demoFixture(t)
	for _, body := range []string{`{"scene":"bot.nope"}`, `{"scenes":["bot.idle","x"]}`, `{}`, `{"scenes":[]}`,
		`{"scene":"bot.idle","scenes":["bot.idle"]}`, `{"scene":"bot.idle","scenes":[],"hold":true}`,
		`{"scene":"bot.idle","extra":1}`} {
		if resp, _ := postDemo(t, f, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", body, resp.StatusCode)
		}
	}
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/knob-000000/demo", testToken, `{"scene":"bot.idle"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown knob = %d %s, want 404", resp.StatusCode, b)
	}
	if st := demoStatusOf(t, f); st.State != demoIdle {
		t.Errorf("status after rejects = %+v", st)
	}
}

func TestDemoDeleteStops(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.idle"}`)
	resp, b := devReq(t, f.srv, "DELETE", "/v1/devices/"+f.m.ID+"/demo", testToken, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", resp.StatusCode, b)
	}
	if d := viewDemo(t, goldenView(t, f)); d != nil {
		t.Fatalf("demo after stop = %+v", d)
	}
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoIdle}) {
		t.Fatalf("status = %+v", st)
	}
}

func TestDemoUnansweredWithinTenSecondsIsDropped(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scenes":["bot.idle","bot.done"]}`)
	f.clk.advance(demoPickup - time.Second)
	if st := demoStatusOf(t, f); st.State != demoPending {
		t.Fatalf("status at 9 s = %+v", st)
	}
	f.clk.advance(time.Second)
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoUnanswered, Scene: "bot.idle", ID: "d0000001", QueueLeft: 1}) {
		t.Fatalf("status at 10 s = %+v", st)
	}
	if d := viewDemo(t, goldenView(t, f)); d != nil {
		t.Fatalf("view still serves %+v", d)
	}
	if resp, _ := f.report(t, "d0000001", "playing"); resp.StatusCode != http.StatusOK {
		t.Fatal(resp.Status)
	}
	if st := demoStatusOf(t, f); st.State != demoUnanswered {
		t.Fatalf("a late report revived it: %+v", st)
	}
}

func TestDemoReportsPlayAndAdvanceTheQueue(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scenes":["bot.idle","bot.done"]}`)
	f.report(t, "d0000001", "playing")
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoPlaying, Scene: "bot.idle", ID: "d0000001", QueueLeft: 1}) {
		t.Fatalf("after playing = %+v", st)
	}
	f.clk.advance(30 * time.Second)
	if st := demoStatusOf(t, f); st.State != demoPlaying {
		t.Fatalf("playing expired by the pickup timer: %+v", st)
	}
	f.report(t, "stale", "done")
	if st := demoStatusOf(t, f); st.ID != "d0000001" {
		t.Fatalf("a stale id advanced the queue: %+v", st)
	}
	_, body := f.report(t, "d0000001", "done")
	if d := viewDemo(t, body); d == nil || d.ID != "d0000002" {
		t.Fatalf("the done reply does not carry the next scene: %s", body)
	}
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoPending, Scene: "bot.done", ID: "d0000002"}) {
		t.Fatalf("after done = %+v", st)
	}
	f.report(t, "d0000002", "playing")
	_, body = f.report(t, "d0000002", "done")
	if d := viewDemo(t, body); d != nil {
		t.Fatalf("demo after the last done = %+v", d)
	}
	if st := demoStatusOf(t, f); st.State != demoIdle {
		t.Fatalf("status after the last done = %+v", st)
	}
}

func TestDemoPlayingWithoutReportsIsDropped(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.idle"}`)
	f.report(t, "d0000001", "playing")
	f.clk.advance(demoSilence - time.Second)
	f.report(t, "d0000001", "playing")
	f.clk.advance(demoSilence - time.Second)
	if st := demoStatusOf(t, f); st.State != demoPlaying {
		t.Fatalf("status = %+v", st)
	}
	f.clk.advance(time.Second)
	if st := demoStatusOf(t, f); st.State != demoIdle {
		t.Fatalf("status after %v silent = %+v", demoSilence, st)
	}
}

func TestDemoViewIsStableWhileItPlays(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"pomodoro.focus"}`)
	resp, _ := f.get(t, "")
	etag := resp.Header.Get("ETag")
	f.clk.advance(3 * time.Second)
	if resp, _ := f.report(t, "d0000001", "playing"); resp.Header.Get("ETag") != etag {
		t.Fatal("the demo view moved with the clock or with a playing report")
	}
}

func TestDemoSetWakesTheViewLongPoll(t *testing.T) {
	f := demoFixture(t)
	resp, _ := f.get(t, "")
	etag := resp.Header.Get("ETag")
	resp, body, after := waitThenAct(t, f, etag, func() { postDemo(t, f, `{"scene":"bot.hop"}`) })
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"act":"hop"`) || after > 5*time.Second {
		t.Fatalf("status %d after %v: %s", resp.StatusCode, after, body)
	}
	etag = resp.Header.Get("ETag")
	resp, body, after = waitThenAct(t, f, etag, func() {
		devReq(t, f.srv, "DELETE", "/v1/devices/"+f.m.ID+"/demo", testToken, "")
	})
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), `"demo"`) || after > 5*time.Second {
		t.Fatalf("stop: status %d after %v: %s", resp.StatusCode, after, body)
	}
}

func TestDemoDoneReportAnswersTheLongPollAtOnce(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scenes":["bot.idle","bot.done"]}`)
	resp, _ := f.get(t, "")
	etag := resp.Header.Get("ETag")
	start := time.Now()
	resp, body := f.getQuery(t, "?wait=20&demo_id=d0000001&demo_state=done", etag)
	if resp.StatusCode != http.StatusOK || time.Since(start) > 5*time.Second {
		t.Fatalf("status %d after %v", resp.StatusCode, time.Since(start))
	}
	if d := viewDemo(t, body); d == nil || d.ID != "d0000002" {
		t.Fatalf("body %s", body)
	}
}

func TestDemoScenesCatalogRoute(t *testing.T) {
	f := demoFixture(t)
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/"+f.m.ID+"/demo/scenes", testToken, "")
	mustOK(t, "scenes", resp, b)
	var got []map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(demoCatalog) {
		t.Fatalf("%d scenes, want %d", len(got), len(demoCatalog))
	}
	if got[0]["name"] != "bot.idle" || got[0]["group"] != demoGroupBot || got[0]["title"] != "Idle" || len(got[0]) != 3 {
		t.Fatalf("first scene = %v", got[0])
	}
	if resp, _ := devReq(t, f.srv, "GET", "/v1/devices/"+f.m.ID+"/demo/scenes", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token = %d", resp.StatusCode)
	}
}

func TestDemoCoverIsFetchableLikeAlbumArt(t *testing.T) {
	f := demoFixture(t)
	versions := map[string]bool{}
	for scene, light := range map[string]bool{"nowplaying.playing": false, "nowplaying.playing_light": true} {
		postDemo(t, f, `{"scene":"`+scene+`"}`)
		d := viewDemo(t, goldenView(t, f))
		var np knobNowPlaying
		if err := json.Unmarshal(d.View["nowplaying"], &np); err != nil {
			t.Fatal(err)
		}
		if np.ArtVersion == "" || np.CoverVersion == "" || !np.AlbumArt || np.Accent == "" || np.CoverLight != light {
			t.Fatalf("%s: nowplaying = %+v", scene, np)
		}
		versions[np.CoverVersion] = true
		for _, q := range []string{"kind=cover&size=466&v=" + np.CoverVersion, "kind=album&size=240&v=" + np.ArtVersion,
			"kind=backdrop&size=466&v=" + np.ArtVersion} {
			resp, b := devReq(t, f.srv, "GET", "/v1/nowplaying/art?"+q, "", "")
			if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || len(b) == 0 {
				t.Errorf("%s: %s = %d %s", scene, q, resp.StatusCode, resp.Header.Get("Content-Type"))
			}
			if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
				t.Errorf("%s: %s Cache-Control = %q", scene, q, resp.Header.Get("Cache-Control"))
			}
		}
	}
	if len(versions) != 2 {
		t.Errorf("the light demo cover shares a version with the dark one: %v", versions)
	}
	if resp, _ := devReq(t, f.srv, "GET", "/v1/nowplaying/art?kind=album&size=240", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unversioned art with nothing playing = %d, want 404", resp.StatusCode)
	}
}

func TestDemoPostLooksUpTheKnobBeforeTheScenes(t *testing.T) {
	f := demoFixture(t)
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/knob-000000/demo", testToken, `{"scene":"bot.nope"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad scene on an unknown knob = %d %s, want 404", resp.StatusCode, b)
	}
}

func TestDemoHoldIsIgnoredWithScenesEvenOne(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scenes":["bot.idle"],"hold":true}`)
	if d := viewDemo(t, goldenView(t, f)); d == nil || d.Hold == nil || *d.Hold {
		t.Fatalf("demo = %+v, want hold false", d)
	}
}

func TestDemoServerEndsAHeldSceneSixtySecondsAfterItStarted(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.idle","hold":true}`)
	f.clk.advance(2 * time.Second)
	f.report(t, "d0000001", "playing")
	for range 2 {
		f.clk.advance(25 * time.Second)
		f.report(t, "d0000001", "playing")
	}
	f.clk.advance(demoHoldCap - 50*time.Second - time.Second)
	if st := demoStatusOf(t, f); st.State != demoPlaying {
		t.Fatalf("status at 59 s = %+v", st)
	}
	f.clk.advance(time.Second)
	f.report(t, "d0000001", "playing")
	if st := demoStatusOf(t, f); st.State != demoIdle {
		t.Fatalf("held scene still up after %v of reports: %+v", demoHoldCap, st)
	}
	if d := viewDemo(t, goldenView(t, f)); d != nil {
		t.Fatalf("view still serves %+v", d)
	}
}

func TestDemoLatePlayingReportForTheServedSceneRevivesIt(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(demoPickup - 100*time.Millisecond)
	if _, body := f.get(t, ""); viewDemo(t, body) == nil {
		t.Fatal("no demo served")
	}
	f.clk.advance(time.Second)
	_, body := f.report(t, "d0000001", "playing")
	if d := viewDemo(t, body); d == nil || d.ID != "d0000001" {
		t.Fatalf("the late report's reply = %s", body)
	}
	if st := demoStatusOf(t, f); st.State != demoPlaying {
		t.Fatalf("status = %+v", st)
	}

	g := demoFixture(t)
	postDemo(t, g, `{"scene":"bot.hop"}`)
	g.get(t, "")
	g.clk.advance(demoPickup + demoGrace)
	g.report(t, "d0000001", "playing")
	if st := demoStatusOf(t, g); st.State != demoUnanswered {
		t.Fatalf("a report after the grace revived it: %+v", st)
	}
}

func TestDemoCountsAsServedOnlyOnceAReplyCarriesIt(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(demoPickup - 100*time.Millisecond)
	if body, _, err := f.app.knobView(f.m.ID, f.clk.Now()); err != nil || viewDemo(t, body) == nil {
		t.Fatalf("view build: %v %s", err, body)
	}
	f.clk.advance(time.Second)
	f.report(t, "d0000001", "playing")
	if st := demoStatusOf(t, f); st.State != demoUnanswered {
		t.Fatalf("a late report revived a scene no reply carried: %+v", st)
	}
}

type hookedViewWriter struct {
	header  http.Header
	code    int
	fail    bool
	onWrite func()
}

func (w *hookedViewWriter) Header() http.Header { return w.header }

func (w *hookedViewWriter) WriteHeader(code int) { w.code = code }

func (w *hookedViewWriter) Write(b []byte) (int, error) {
	if w.onWrite != nil {
		w.onWrite()
	}
	if w.fail {
		return 0, errors.New("broken pipe")
	}
	return len(b), nil
}

func serveViewThrough(t *testing.T, f *viewFixture, w *hookedViewWriter) {
	t.Helper()
	req := httptest.NewRequest("GET", knobViewPath, nil)
	req.Header.Set("Authorization", "Bearer "+f.m.Token)
	f.srv.Config.Handler.ServeHTTP(w, req)
	if w.code != http.StatusOK {
		t.Fatalf("view = %d", w.code)
	}
}

func lateDemoState(t *testing.T, f *viewFixture) string {
	t.Helper()
	f.report(t, "d0000001", "playing")
	return demoStatusOf(t, f).State
}

func TestDemoReportRacingTheReplyThatCarriedTheSceneCounts(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(demoPickup - 100*time.Millisecond)
	var raced string
	serveViewThrough(t, f, &hookedViewWriter{header: http.Header{}, onWrite: func() {
		f.clk.advance(time.Second)
		raced = lateDemoState(t, f)
	}})
	if raced != demoPlaying {
		t.Fatalf("a late playing report handled while its reply was being written was rejected: %s", raced)
	}
}

func TestDemoReplyThatFailedToWriteIsNotServed(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(demoPickup - 100*time.Millisecond)
	serveViewThrough(t, f, &hookedViewWriter{header: http.Header{}, fail: true})
	f.clk.advance(time.Second)
	if st := lateDemoState(t, f); st != demoUnanswered {
		t.Fatalf("a reply that never reached the knob let a late report revive the scene: %s", st)
	}
}

func TestDemoNotModifiedReplyCountsAsServed(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(demoPickup - 100*time.Millisecond)
	_, etag, err := f.app.knobView(f.m.ID, f.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := f.get(t, etag); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional view = %d", resp.StatusCode)
	}
	f.clk.advance(time.Second)
	if st := lateDemoState(t, f); st != demoPlaying {
		t.Fatalf("a 304 for the view holding the scene did not count as served: %s", st)
	}
}

func TestDemoLongPollThatDeliversTheSceneCountsAsServed(t *testing.T) {
	f := demoFixture(t)
	resp, _ := f.get(t, "")
	_, body, _ := waitThenAct(t, f, resp.Header.Get("ETag"), func() { postDemo(t, f, `{"scene":"bot.hop"}`) })
	if viewDemo(t, body) == nil {
		t.Fatalf("long poll woke without the scene: %s", body)
	}
	f.clk.advance(demoPickup + time.Second)
	if st := lateDemoState(t, f); st != demoPlaying {
		t.Fatalf("a long-poll reply carrying the scene did not count as served: %s", st)
	}
}

func TestDemoLongPollTheKnobLeftIsNotServed(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	_, etag, err := f.app.knobView(f.m.ID, f.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _, _ = f.wait(ctx, t, etag, "10")
	}()
	waitForWaiters(t, f.app, 1)
	cancel()
	<-done
	waitForWaiters(t, f.app, 0)
	f.clk.advance(demoPickup + time.Second)
	if st := lateDemoState(t, f); st != demoUnanswered {
		t.Fatalf("a long poll that never answered let a late report revive the scene: %s", st)
	}
}

func TestDemoLateDoneReportForTheServedSceneAdvancesTheQueue(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scenes":["bot.hop","bot.done"]}`)
	f.clk.advance(demoPickup - 100*time.Millisecond)
	if _, body := f.get(t, ""); viewDemo(t, body) == nil {
		t.Fatal("no demo served")
	}
	f.clk.advance(time.Second)
	if st := demoStatusOf(t, f); st.State != demoUnanswered {
		t.Fatalf("status before the late report = %+v", st)
	}
	_, body := f.report(t, "d0000001", "done")
	if d := viewDemo(t, body); d == nil || d.ID != "d0000002" {
		t.Fatalf("the late done's reply = %s", body)
	}
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoPending, Scene: "bot.done", ID: "d0000002"}) {
		t.Fatalf("status = %+v", st)
	}

	g := demoFixture(t)
	postDemo(t, g, `{"scenes":["bot.hop","bot.done"]}`)
	goldenView(t, g)
	g.clk.advance(demoPickup + demoGrace)
	g.report(t, "d0000001", "done")
	if st := demoStatusOf(t, g); st.State != demoUnanswered || st.ID != "d0000001" {
		t.Fatalf("a done after the grace advanced it: %+v", st)
	}
}

func TestDemoUnansweredFadesToIdle(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.idle"}`)
	f.clk.advance(demoPickup)
	if st := demoStatusOf(t, f); st.State != demoUnanswered {
		t.Fatalf("status = %+v", st)
	}
	f.clk.advance(demoForget - time.Second)
	if st := demoStatusOf(t, f); st.State != demoUnanswered {
		t.Fatalf("status before it fades = %+v", st)
	}
	f.clk.advance(time.Second)
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoIdle}) {
		t.Fatalf("status %v after unanswered = %+v", demoForget, st)
	}
}

func TestDemoPlayingReportDoesNotAnswerTheLongPoll(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scene":"bot.idle"}`)
	resp, _ := f.get(t, "")
	etag := resp.Header.Get("ETag")
	for _, label := range []string{"first", "repeated"} {
		start := time.Now()
		resp, body := f.getQuery(t, "?wait=2&demo_id=d0000001&demo_state=playing", etag)
		if resp.StatusCode != http.StatusNotModified || time.Since(start) < 1500*time.Millisecond {
			t.Fatalf("%s playing report: status %d after %v: %s", label, resp.StatusCode, time.Since(start), body)
		}
	}
	if st := demoStatusOf(t, f); st.State != demoPlaying {
		t.Fatalf("status = %+v", st)
	}
}

func TestDemoOnTheWorstCaseViewFitsTheBuffer(t *testing.T) {
	f := goldenViewFixture(t)
	worstCaseView(t, f)
	plantCaps(t, f, knobCapsDemo.Clone())
	f.app.demos.newID = func() string { return strings.Repeat("f", 8) }
	worst, size := "", 0
	for _, sc := range demoCatalog {
		f.app.demos.start(f.m.ID, []string{sc.Name}, true, f.clk.Now())
		body := goldenView(t, f)
		if viewDemo(t, body) == nil {
			t.Fatalf("%s: no demo", sc.Name)
		}
		if w := decodeFaces(t, body).Weather; w.Moon == nil || w.Rain == nil {
			t.Fatalf("%s: the real view lost its weather detail to fit", sc.Name)
		}
		if len(body) > size {
			worst, size = sc.Name, len(body)
		}
	}
	t.Logf("worst demo view: %s, %d B", worst, size)
	if size >= knobCapsDemo.Limits.ViewBytes {
		t.Fatalf("%s on the worst-case view is %d B, over the knob's %d B view buffer", worst, size, knobCapsDemo.Limits.ViewBytes)
	}
}

func demoCheckin(t *testing.T, f *viewFixture, uptime, boots int64) {
	t.Helper()
	body := map[string]any{"fw": "0.9.70", "caps": knobCapsDemo}
	if uptime > 0 {
		body["uptime_s"] = uptime
	}
	if boots > 0 {
		body["diag"] = map[string]any{"boots": boots}
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, rb := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, string(b))
	mustOK(t, "checkin", resp, rb)
}

func playDemo(t *testing.T, f *viewFixture, body string) {
	t.Helper()
	postDemo(t, f, body)
	goldenView(t, f)
	f.report(t, "d0000001", "playing")
}

func TestDemoKnobRebootDropsTheDemoAndItsQueue(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	playDemo(t, f, `{"scenes":["bot.hop","bot.done"]}`)
	f.clk.advance(2 * time.Second)
	demoCheckin(t, f, 1, 41)
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoRestarted, Scene: "bot.hop", ID: "d0000001"}) {
		t.Fatalf("status after the reboot = %+v", st)
	}
	if d := viewDemo(t, goldenView(t, f)); d != nil {
		t.Fatalf("the rebooted knob is served %+v again", d)
	}
	for _, state := range []string{"playing", "done"} {
		if _, body := f.report(t, "d0000001", state); viewDemo(t, body) != nil {
			t.Fatalf("a %s report after the reboot brought the demo back: %s", state, body)
		}
	}
	if st := demoStatusOf(t, f); st.State != demoRestarted {
		t.Fatalf("a report after the reboot moved the status: %+v", st)
	}
	f.clk.advance(demoForget - time.Second)
	if st := demoStatusOf(t, f); st.State != demoRestarted {
		t.Fatalf("status before it fades = %+v", st)
	}
	f.clk.advance(time.Second)
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoIdle}) {
		t.Fatalf("status %v after the reboot = %+v", demoForget, st)
	}
}

func TestDemoKnobCrashingRightAfterPlayingIsDroppedAtItsFirstCheckin(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	playDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(2 * time.Second)
	demoCheckin(t, f, 1, 41)
	if st := demoStatusOf(t, f); st.State != demoRestarted {
		t.Fatalf("a knob that crashed in the scene and checked in 2 s after it started kept the demo: %+v", st)
	}
}

func TestDemoKnobRebootIsSeenFromBootsWhenUptimeGrew(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 2, 40)
	playDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(10 * time.Second)
	demoCheckin(t, f, 5, 41)
	if st := demoStatusOf(t, f); st.State != demoRestarted {
		t.Fatalf("status after a reboot with a higher uptime = %+v", st)
	}
}

func TestDemoKnobRebootWhileThePendingSceneWaitsDropsIt(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	postDemo(t, f, `{"scenes":["bot.hop","bot.done"]}`)
	f.clk.advance(4 * time.Second)
	demoCheckin(t, f, 1, 41)
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoRestarted, Scene: "bot.hop", ID: "d0000001"}) {
		t.Fatalf("status = %+v", st)
	}
	if d := viewDemo(t, goldenView(t, f)); d != nil {
		t.Fatalf("served %+v", d)
	}
}

func TestDemoKnobRebootKeepsAnUnansweredRunUnanswered(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	postDemo(t, f, `{"scenes":["bot.hop","bot.done"]}`)
	f.clk.advance(11 * time.Second)
	demoCheckin(t, f, 1, 41)
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoUnanswered, Scene: "bot.hop", ID: "d0000001", QueueLeft: 1}) {
		t.Fatalf("status = %+v", st)
	}
}

func TestDemoCheckinWithoutARebootKeepsTheDemo(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	playDemo(t, f, `{"scene":"bot.hop"}`)
	demoCheckin(t, f, 830, 40)
	if st := demoStatusOf(t, f); st.State != demoPlaying {
		t.Fatalf("status after a plain checkin = %+v", st)
	}
	if _, body := f.report(t, "d0000001", "playing"); viewDemo(t, body) == nil {
		t.Fatalf("the scene is gone: %s", body)
	}
}

func TestDemoCheckinWithUnknownOrStaleUptimeKeepsTheDemo(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prev, next [2]int64
	}{
		{"no uptime, same boots", [2]int64{800, 40}, [2]int64{0, 40}},
		{"no uptime, no diag", [2]int64{800, 40}, [2]int64{0, 0}},
		{"out of order, same boots", [2]int64{830, 40}, [2]int64{800, 40}},
		{"out of order across a reboot", [2]int64{30, 41}, [2]int64{800, 40}},
		{"out of order, no diag", [2]int64{830, 0}, [2]int64{800, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := demoFixture(t)
			demoCheckin(t, f, tc.prev[0], tc.prev[1])
			playDemo(t, f, `{"scene":"bot.hop"}`)
			f.clk.advance(time.Second)
			demoCheckin(t, f, tc.next[0], tc.next[1])
			if st := demoStatusOf(t, f); st.State != demoPlaying {
				t.Fatalf("status = %+v", st)
			}
		})
	}
}

func TestDemoStartedAfterTheKnobBootedSurvivesAStaleLastCheckin(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	f.clk.advance(10 * time.Minute)
	playDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(5 * time.Second)
	demoCheckin(t, f, 30, 43)
	if st := demoStatusOf(t, f); st.State != demoPlaying {
		t.Fatalf("a demo started after the boot was dropped by the first checkin: %+v", st)
	}
	if _, body := f.report(t, "d0000001", "playing"); viewDemo(t, body) == nil {
		t.Fatalf("the scene is gone: %s", body)
	}
}

func TestDemoSetBetweenBootAndFirstCheckinStaysPending(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	f.clk.advance(time.Minute)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(time.Second)
	demoCheckin(t, f, 4, 41)
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoPending, Scene: "bot.hop", ID: "d0000001"}) {
		t.Fatalf("status = %+v", st)
	}
	if d := viewDemo(t, goldenView(t, f)); d == nil {
		t.Fatal("the knob is not served the demo set after it booted")
	}
}

func TestDemoStartedJustAfterTheEstimatedBootSurvives(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	f.clk.advance(time.Minute)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(1500 * time.Millisecond)
	demoCheckin(t, f, 1, 41)
	if st := demoStatusOf(t, f); st.State != demoPending {
		t.Fatalf("a demo set within the boot estimate's error (uptime truncation, transit) was dropped: %+v", st)
	}
}

func TestDemoRebootWithUnknownUptimeKeepsTheDemo(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	playDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(5 * time.Second)
	demoCheckin(t, f, 0, 41)
	if st := demoStatusOf(t, f); st.State != demoPlaying {
		t.Fatalf("a reboot checkin without uptime_s dropped the demo with an unknown boot time: %+v", st)
	}
}

func TestStaleCheckinKeepsTheNewerLastCheckin(t *testing.T) {
	for _, tc := range []struct {
		name        string
		boots       int64
		gap         time.Duration
		wantUptime  int64
		description string
	}{
		{"same boots", 40, time.Second, 830, "an older checkin arriving late replaced the newer one"},
		{"no diag", 0, time.Second, 830, "an older checkin arriving late replaced the newer one"},
		{"long after", 40, 2 * time.Minute, 800, "a lower uptime minutes later must still be stored"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := demoFixture(t)
			demoCheckin(t, f, 830, tc.boots)
			f.clk.advance(tc.gap)
			demoCheckin(t, f, 800, tc.boots)
			if got := f.app.devices.List()[0].LastCheckin.UptimeS; got != tc.wantUptime {
				t.Fatalf("stored uptime = %d, want %d: %s", got, tc.wantUptime, tc.description)
			}
		})
	}
	f := demoFixture(t)
	demoCheckin(t, f, 30, 41)
	f.clk.advance(time.Second)
	demoCheckin(t, f, 800, 40)
	if got := f.app.devices.List()[0].LastCheckin.Diag.Boots; got != 41 {
		t.Fatalf("stored boots = %d, want 41: a checkin from before the reboot replaced the newer one", got)
	}
}

func TestStaleCheckinDoesNotRollBackCaps(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 30, 41)
	f.clk.advance(2 * time.Second)
	old := knobCapsDemo.Clone()
	old.Features = []string{featureNPCover}
	b, err := json.Marshal(map[string]any{"fw": "0.9.60", "uptime_s": 800, "diag": map[string]any{"boots": 40}, "caps": old})
	if err != nil {
		t.Fatal(err)
	}
	resp, rb := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, string(b))
	mustOK(t, "stale checkin", resp, rb)
	st, err := f.app.devices.ViewState(f.m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Caps.HasFeature(featureDemo) {
		t.Fatalf("a checkin from before the reboot replaced the newer caps: %+v", st.Caps)
	}
}

func TestStaleCheckinWithoutBootsAfterABootIsNotStored(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 5, 0)
	f.clk.advance(time.Second)
	demoCheckin(t, f, 3000, 0)
	if got := f.app.devices.List()[0].LastCheckin.UptimeS; got != 5 {
		t.Fatalf("stored uptime = %d, want 5: a pre-boot checkin arriving late replaced the post-boot one", got)
	}
}

func TestCheckinOnTheSameBootWithGrownUptimeIsStored(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 5, 40)
	f.clk.advance(time.Second)
	demoCheckin(t, f, 3000, 40)
	if got := f.app.devices.List()[0].LastCheckin.UptimeS; got != 3000 {
		t.Fatalf("stored uptime = %d, want 3000: on the same boot a grown uptime can only be the newer checkin", got)
	}
}

func TestDemoRebootSparesARunStartedAfterTheRebootWasRead(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	d := &demoRegistry{}
	d.start("k", []string{"bot.hop"}, false, t0)
	gen := d.generation("k")
	d.start("k", []string{"bot.done"}, false, t0)
	if d.knobRestarted("k", gen, t0.Add(3*time.Second), t0.Add(4*time.Second)) {
		t.Fatal("a reboot read before the newer run started dropped it")
	}
	if st := d.status("k", t0.Add(4*time.Second)); st.State != demoPending || st.Scene != "bot.done" {
		t.Fatalf("status = %+v", st)
	}
	if !d.knobRestarted("k", d.generation("k"), t0.Add(3*time.Second), t0.Add(4*time.Second)) {
		t.Fatal("the run of the same generation that started before the boot was kept")
	}
	if d.knobRestarted("k", 0, t0.Add(3*time.Second), t0.Add(4*time.Second)) {
		t.Fatal("restarted twice")
	}
}

func TestDemoViewWithoutAReportWhilePlayingMeansTheKnobRestarted(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scenes":["bot.hop","bot.done"]}`)
	for _, label := range []string{"first", "repeated"} {
		if _, body := f.get(t, ""); viewDemo(t, body) == nil {
			t.Fatalf("%s unreported view before playing: no scene: %s", label, body)
		}
	}
	f.report(t, "d0000001", "playing")
	f.clk.advance(4 * time.Second)
	if _, body := f.get(t, ""); viewDemo(t, body) != nil {
		t.Fatalf("a view without a report while playing was served the scene: %s", body)
	}
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoRestarted, Scene: "bot.hop", ID: "d0000001"}) {
		t.Fatalf("status = %+v", st)
	}
}

func TestDemoDelayedUnreportedViewAfterPlayingKeepsTheDemo(t *testing.T) {
	f := demoFixture(t)
	postDemo(t, f, `{"scenes":["bot.hop","bot.done"]}`)
	goldenView(t, f)
	f.report(t, "d0000001", "playing")
	for _, step := range []time.Duration{0, 2 * time.Second} {
		f.clk.advance(step)
		if _, body := f.get(t, ""); viewDemo(t, body) == nil {
			t.Fatalf("a delayed request sent before playing, arriving %v after the report, ended the demo: %s", step, body)
		}
	}
	if st := demoStatusOf(t, f); st != (demoStatus{State: demoPlaying, Scene: "bot.hop", ID: "d0000001", QueueLeft: 1}) {
		t.Fatalf("status = %+v", st)
	}
	f.report(t, "d0000001", "playing")
	if _, body := f.get(t, ""); viewDemo(t, body) == nil {
		t.Fatalf("a delayed request right after a later report ended the demo: %s", body)
	}
}

func TestDemoKnobRestartAnswersTheLongPollAtOnce(t *testing.T) {
	f := demoFixture(t)
	demoCheckin(t, f, 800, 40)
	postDemo(t, f, `{"scene":"bot.hop"}`)
	f.clk.advance(4 * time.Second)
	resp, _ := f.get(t, "")
	etag := resp.Header.Get("ETag")
	resp, body, after := waitThenAct(t, f, etag, func() { demoCheckin(t, f, 1, 41) })
	if resp.StatusCode != http.StatusOK || viewDemo(t, body) != nil || after > 5*time.Second {
		t.Fatalf("status %d after %v: %s", resp.StatusCode, after, body)
	}
}
