package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var knobCapsDemo = &deviceCaps{
	View:     []int{1, 1},
	Pages:    []string{"bot", "pomodoro", "weather", "nowplaying"},
	Features: []string{featureDemo, featureNPCover, featureViewWait, featureWxFaces, featureWxMoments},
	Limits:   &deviceCapsLimits{ViewBytes: 16383, ConfigBytes: 1024},
}

func demoFixture(t *testing.T) *viewFixture {
	t.Helper()
	f := goldenViewFixture(t)
	f.app.viewClock = f.clk.Now
	f.app.viewRecheck = time.Hour
	plantCaps(t, f, knobCapsDemo.clone())
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
	want := map[string]int{demoGroupBot: 13, demoGroupMoments: 18, demoGroupFaces: 78, demoGroupPomo: 7, demoGroupNP: 4}
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
	for _, name := range []string{"bot.sleepy", "bot.chase_half", "bot.offline", "bot.quiet_calm", "moment.afterrain.quiet",
		"face.air.fog", "face.days.dry", "sun.polar_night", "pomodoro.parked", "pomodoro.done", "nowplaying.nothing"} {
		if !seen[name] {
			t.Errorf("scene %q missing", name)
		}
	}
}

var (
	demoPagePattern = regexp.MustCompile(`^(bot|pomodoro|nowplaying|weather|weather:(temp|rain|cast|wind|days|sun|air))$`)
	demoActPattern  = regexp.MustCompile(`^(moment:(glance|night|frost|heat|wind|afterrain|snow|rain|storm):(full|quiet)|chase:(full|half)|hop|pop|blink)$`)
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
	caps := knobCapsDemo.clone()
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
		`{"scene":"bot.idle","scenes":["bot.idle"]}`, `{"scene":"bot.idle","extra":1}`} {
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
	postDemo(t, f, `{"scene":"bot.idle","hold":true}`)
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
	postDemo(t, f, `{"scene":"nowplaying.playing"}`)
	d := viewDemo(t, goldenView(t, f))
	var np knobNowPlaying
	if err := json.Unmarshal(d.View["nowplaying"], &np); err != nil {
		t.Fatal(err)
	}
	if np.ArtVersion == "" || np.CoverVersion == "" || !np.AlbumArt || np.Accent == "" {
		t.Fatalf("nowplaying = %+v", np)
	}
	for _, q := range []string{"kind=cover&size=466&v=" + np.CoverVersion, "kind=album&size=240&v=" + np.ArtVersion,
		"kind=backdrop&size=466&v=" + np.ArtVersion} {
		resp, b := devReq(t, f.srv, "GET", "/v1/nowplaying/art?"+q, "", "")
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || len(b) == 0 {
			t.Errorf("%s = %d %s", q, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
			t.Errorf("%s Cache-Control = %q", q, resp.Header.Get("Cache-Control"))
		}
	}
	if resp, _ := devReq(t, f.srv, "GET", "/v1/nowplaying/art?kind=album&size=240", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unversioned art with nothing playing = %d, want 404", resp.StatusCode)
	}
}
