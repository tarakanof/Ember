package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/devices"
	"github.com/tarakanof/ember/internal/pomodoro"
	"github.com/tarakanof/ember/internal/printer"
)

var knobCapsPrint = map[string]any{
	"view":     []int{1, 1},
	"pages":    []string{"bot", "print"},
	"features": []string{"print_moments", "view_wait"},
	"limits":   map[string]any{"view_bytes": 16383, "config_bytes": 2048},
}

const printThumbRef = "/thumb/l/usb/BRACKE~1.BGC"

type fakePrinter struct {
	srv *httptest.Server

	mu     sync.Mutex
	status string
	job    string
	thumb  []byte
	hits   map[string]int
}

type fakeJob struct {
	id        int
	progress  float64
	remaining float64
	printing  float64
	change    float64
}

func newFakePrinter(t *testing.T) *fakePrinter {
	t.Helper()
	fp := &fakePrinter{hits: map[string]int{}}
	fp.setStatus("IDLE", nil, 24, 0, 23, 0)
	fp.srv = httptest.NewServer(http.HandlerFunc(fp.serve))
	t.Cleanup(fp.srv.Close)
	return fp
}

func (fp *fakePrinter) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), `Digest username="maker"`) {
		w.Header().Set("WWW-Authenticate", `Digest realm="Printer API", nonce="0123456789abcdef", stale=false`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.hits[r.URL.Path]++
	switch r.URL.Path {
	case "/api/v1/status":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fp.status))
	case "/api/v1/job":
		if fp.job == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fp.job))
	case "/api/version":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hostname":"prusa-core-one","firmware":"8.0.0"}`))
	case printThumbRef:
		if fp.thumb == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(fp.thumb)
	default:
		http.NotFound(w, r)
	}
}

func (fp *fakePrinter) setStatus(state string, j *fakeJob, nozzle, nozzleT, bed, bedT float64) {
	pr := fmt.Sprintf(`"printer":{"state":%q,"temp_nozzle":%g,"target_nozzle":%g,"temp_bed":%g,"target_bed":%g}`, state, nozzle, nozzleT, bed, bedT)
	body := "{" + pr + "}"
	if j != nil {
		js := fmt.Sprintf(`"job":{"id":%d,"progress":%g,"time_remaining":%g,"time_printing":%g`, j.id, j.progress, j.remaining, j.printing)
		if j.change > 0 {
			js += fmt.Sprintf(`,"filament_change_in":%g`, j.change)
		}
		body = "{" + js + "}," + pr + "}"
	}
	fp.mu.Lock()
	fp.status = body
	fp.mu.Unlock()
}

func (fp *fakePrinter) setJob(id int, state, display string, thumb []byte) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.thumb = thumb
	if id == 0 {
		fp.job = ""
		return
	}
	fp.job = fmt.Sprintf(`{"id":%d,"state":%q,"file":{"refs":{"thumbnail":%q},"name":"BRACKE~1.BGC","display_name":%q}}`, id, state, printThumbRef, display)
}

func (fp *fakePrinter) hit(path string) int {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.hits[path]
}

type printFixture struct {
	*viewFixture
	fp *fakePrinter
}

func newPrintFixture(t *testing.T) *printFixture {
	t.Helper()
	f := goldenViewFixture(t)
	fp := newFakePrinter(t)
	f.app.printer = f.app.newPrinter(nil, f.clk.Now)
	f.app.updateConfig(func(c *Config) {
		c.Printer.Enabled = true
		c.Printer.URL = fp.srv.URL
	})
	f.app.configurePrinter(printer.Credentials{User: "maker", Password: printTestPassword})
	return &printFixture{viewFixture: f, fp: fp}
}

func (p *printFixture) step(t *testing.T, d time.Duration) {
	t.Helper()
	p.clk.advance(d)
	p.app.printer.Step(context.Background())
}

func (p *printFixture) printing(t *testing.T, thumb []byte) {
	t.Helper()
	p.step(t, 0)
	p.fp.setJob(412, "PRINTING", "bracket_v3_0.4n_0.2mm_PETG_COREONE_2h14m.bgcode", thumb)
	p.fp.setStatus("PRINTING", &fakeJob{id: 412, progress: 0, remaining: 8040, printing: 5}, 120, 240, 60, 85)
	p.step(t, 5*time.Second)
	p.fp.setStatus("PRINTING", &fakeJob{id: 412, progress: 24.6, remaining: 6540, printing: 1500, change: 960}, 240.2, 240, 85.1, 85)
	p.step(t, 25*time.Minute)
}

func knobPrintOn(t *testing.T, f *viewFixture) {
	t.Helper()
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"print","on":true}],"home":"bot"}`)
}

func viewPrint(t *testing.T, body []byte) (map[string]any, bool) {
	t.Helper()
	var v struct {
		Print map[string]any `json:"print"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v.Print, v.Print != nil
}

func TestKnobViewPrintGolden(t *testing.T) {
	p := newPrintFixture(t)
	knobPrintOn(t, p.viewFixture)
	postCapsCheckin(t, p.viewFixture, "0.10.0", knobCapsPrint)
	p.printing(t, pngBytes(t, 32, color.RGBA{0xFA, 0x68, 0x31, 255}))
	assertDeviceGolden(t, "view_print", goldenView(t, p.viewFixture))
}

func TestKnobViewPrintAttentionGolden(t *testing.T) {
	p := newPrintFixture(t)
	knobPrintOn(t, p.viewFixture)
	postCapsCheckin(t, p.viewFixture, "0.10.0", knobCapsPrint)
	p.printing(t, pngBytes(t, 32, color.RGBA{0xFA, 0x68, 0x31, 255}))
	p.fp.setStatus("ATTENTION", &fakeJob{id: 412, progress: 24.9, remaining: 6480, printing: 1560, change: 0}, 240, 240, 85, 85)
	p.step(t, time.Minute)
	body := goldenView(t, p.viewFixture)
	if pr, _ := viewPrint(t, body); pr["phase"] != "attention" || pr["event"].(map[string]any)["kind"] != "attention" {
		t.Fatalf("attention block = %v", pr)
	}
	assertDeviceGolden(t, "view_print_attention", body)
}

func TestKnobViewPrintHeatingGolden(t *testing.T) {
	p := newPrintFixture(t)
	knobPrintOn(t, p.viewFixture)
	postCapsCheckin(t, p.viewFixture, "0.10.0", knobCapsPrint)
	p.step(t, 0)
	p.fp.setJob(412, "PRINTING", "bracket_v3_0.4n_0.2mm_PETG_COREONE_2h14m.bgcode", nil)
	p.fp.setStatus("PRINTING", &fakeJob{id: 412, progress: 0, remaining: 8040, printing: 40}, 182.4, 240, 71.6, 85)
	p.step(t, 5*time.Second)
	body := goldenView(t, p.viewFixture)
	if pr, _ := viewPrint(t, body); pr["phase"] != "heating" || pr["nozzle"] != 182.0 || pr["thumb_version"] != nil {
		t.Fatalf("heating block = %v", pr)
	}
	assertDeviceGolden(t, "view_print_heating", body)
}

func TestKnobViewSendsPrintOnlyToCapsKnobsWithThePageOrMoments(t *testing.T) {
	p := newPrintFixture(t)
	f := p.viewFixture
	p.printing(t, nil)
	knobPrintOn(t, f)
	legacy := goldenView(t, f)
	if _, ok := viewPrint(t, legacy); ok {
		t.Fatalf("legacy knob with the print page on got print: %s", legacy)
	}
	p.app.updateConfig(func(c *Config) { c.Printer.Enabled = false })
	p.step(t, 5*time.Second)
	if off := goldenView(t, f); !bytes.Equal(off, legacy) {
		t.Fatalf("legacy view changed with the printer:\n%s\n%s", legacy, off)
	}
	p.app.updateConfig(func(c *Config) { c.Printer.Enabled = true })
	p.step(t, 5*time.Second)

	cases := []struct {
		name  string
		caps  map[string]any
		pages string
		want  bool
	}{
		{"page on but no longer in caps", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "weather"}, "features": []string{}},
			`{"pages":[{"id":"bot","on":true},{"id":"print","on":true}]}`, false},
		{"page in caps but off", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "print"}, "features": []string{}},
			`{"pages":[{"id":"bot","on":true},{"id":"print","on":false}]}`, false},
		{"page in caps and on", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "print"}, "features": []string{}},
			`{"pages":[{"id":"bot","on":true},{"id":"print","on":true}]}`, true},
		{"moments with the bot page on", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "print"}, "features": []string{"print_moments"}},
			`{"pages":[{"id":"bot","on":true},{"id":"print","on":false}]}`, true},
		{"moments on firmware without the print page", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "weather"}, "features": []string{"print_moments"}},
			`{"pages":[{"id":"bot","on":true},{"id":"weather","on":true}],"home":"bot"}`, true},
		{"moments turned off", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "print"}, "features": []string{"print_moments"}},
			`{"pages":[{"id":"bot","on":true},{"id":"print","on":false}],"bot":{"print_moments":false}}`, false},
		{"moments with the bot page off", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "weather", "print"}, "features": []string{"print_moments"}},
			`{"pages":[{"id":"bot","on":false},{"id":"weather","on":true},{"id":"print","on":false}],"home":"weather","bot":{"print_moments":true}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "weather", "print"}, "features": []string{}})
			putKnobConfig(t, f.srv, f.m.ID, `{"bot":{"print_moments":null}}`)
			putKnobConfig(t, f.srv, f.m.ID, c.pages)
			postCapsCheckin(t, f, "0.10.0", c.caps)
			body := goldenView(t, f)
			if _, ok := viewPrint(t, body); ok != c.want {
				t.Fatalf("print sent = %v, want %v: %s", ok, c.want, body)
			}
		})
	}

	knobPrintOn(t, f)
	postCapsCheckin(t, f, "0.10.0", knobCapsPrint)
	p.app.updateConfig(func(c *Config) { c.Printer.Enabled = false })
	p.step(t, 5*time.Second)
	if body := goldenView(t, f); strings.Contains(string(body), `"print"`) {
		t.Fatalf("disabled printer still sends print: %s", body)
	}
	p.app.updateConfig(func(c *Config) { c.Printer.Enabled = true })
	p.app.configurePrinter(printer.Credentials{User: "maker"})
	p.step(t, 5*time.Second)
	if body := goldenView(t, f); strings.Contains(string(body), `"print"`) {
		t.Fatalf("printer without a password still sends print: %s", body)
	}
}

func TestKnobViewPrintBlockFollowsThePhase(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ev := &printer.Event{ID: 1793880000000, Kind: printer.EventFinished, At: now.Add(-time.Minute), JobID: 412}
	base := printer.Snapshot{Enabled: true, Progress: 100, EndsAt: now, StartedAt: now.Add(-2 * time.Hour),
		Nozzle: 200, NozzleTarget: 240, Bed: 60, BedTarget: 85, Name: "bracket_v3", Material: "PETG",
		ThumbVersion: "1a2b3c4d5e6f", Event: ev, Printer: printer.Info{Name: "CORE One"}}
	enc := func(s printer.Snapshot) string {
		b, err := json.Marshal(newKnobPrint(s))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, c := range []struct {
		phase printer.Phase
		want  string
	}{
		{printer.PhaseOff, `null`},
		{printer.PhaseOffline, `{"phase":"offline","event":{"id":1793880000000,"kind":"finished","at":1793879940},"printer":"CORE One"}`},
		{printer.PhaseIdle, `{"phase":"idle","event":{"id":1793880000000,"kind":"finished","at":1793879940},"printer":"CORE One"}`},
		{printer.PhaseHeating, `{"phase":"heating","progress":100,"ends_at":1793880000,"started_at":1793872800,"nozzle":200,"nozzle_target":240,"bed":60,"bed_target":85,"name":"bracket_v3","material":"PETG","thumb_version":"1a2b3c4d5e6f-r1","event":{"id":1793880000000,"kind":"finished","at":1793879940},"printer":"CORE One"}`},
		{printer.PhaseFinished, `{"phase":"finished","progress":100,"ends_at":1793880000,"started_at":1793872800,"name":"bracket_v3","material":"PETG","thumb_version":"1a2b3c4d5e6f-r1","event":{"id":1793880000000,"kind":"finished","at":1793879940},"printer":"CORE One"}`},
	} {
		s := base
		s.Phase = c.phase
		if got := enc(s); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.phase, got, c.want)
		}
	}
	s := base
	s.Phase, s.Enabled = printer.PhasePrinting, false
	if got := enc(s); got != "null" {
		t.Errorf("disabled printer: %s", got)
	}
}

func TestKnobViewPrintBlockStaysSmall(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ev := &printer.Event{ID: 1793880000000, Kind: printer.EventAttention, At: now}
	typical := printer.Snapshot{Enabled: true, Phase: printer.PhaseAttention, Progress: 42, EndsAt: now.Add(time.Hour),
		StartedAt: now.Add(-time.Hour), ChangeAt: now.Add(10 * time.Minute), Name: "bracket_v3_with_a_long_name_x", Material: "PETG",
		ThumbVersion: "1a2b3c4d5e6f", Event: ev, Printer: printer.Info{Name: "CORE One"}}
	b, err := json.Marshal(newKnobPrint(typical))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= 300 {
		t.Fatalf("typical block is %d B, want under 300: %s", len(b), b)
	}
	worst := typical
	worst.Phase = printer.PhaseHeating
	worst.Nozzle, worst.NozzleTarget, worst.Bed, worst.BedTarget = 290, 290, 120, 120
	worst.Name = strings.Repeat("<", printer.MaxNameRunes)
	worst.Material = strings.Repeat("&", 40)
	worst.Printer.Name = strings.Repeat("\x01", 64)
	b, err = json.Marshal(newKnobPrint(worst))
	if err != nil {
		t.Fatal(err)
	}
	var back knobPrint
	if err := json.Unmarshal(b, &back); err != nil || len([]rune(back.Material)) != knobPrintMaterialRunes || len([]rune(back.Printer)) != knobPrintLabelRunes {
		t.Fatalf("worst block = %s (%v)", b, err)
	}
	if len(b)+len(`,"thumb_rows":[110,300]`) > 832 {
		t.Fatalf("worst block is %d B before thumb_rows, want at most 832 with them: %s", len(b), b)
	}
	t.Logf("worst block: %d B", len(b))
}

func TestKnobViewPrintETagMovesOnlyOnWholePercentsAndPhases(t *testing.T) {
	p := newPrintFixture(t)
	knobPrintOn(t, p.viewFixture)
	postCapsCheckin(t, p.viewFixture, "0.10.0", knobCapsPrint)
	p.step(t, 0)
	p.fp.setJob(9, "PRINTING", "lid_0.4n_0.2mm_PLA_COREONE_2h0m.bgcode", pngBytes(t, 16, color.White))
	polls, changes, last := 0, 0, ""
	phases := map[string]bool{}
	poll := func() {
		p.step(t, printer.PollActive)
		_, etag, err := p.app.knobView(p.m.ID, p.clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		polls++
		if etag != last {
			changes++
			last = etag
		}
		if pr, ok := viewPrint(t, goldenView(t, p.viewFixture)); ok {
			phases[pr["phase"].(string)] = true
		}
	}
	const job = 2 * time.Hour
	elapsed := time.Duration(0)
	for i := range 24 {
		p.fp.setStatus("PRINTING", &fakeJob{id: 9, remaining: job.Seconds(), printing: elapsed.Seconds()}, 25+9*float64(i), 230, 24+2.5*float64(i), 60)
		poll()
		elapsed += printer.PollActive
	}
	for range 24 {
		p.fp.setStatus("PRINTING", &fakeJob{id: 9, remaining: job.Seconds(), printing: elapsed.Seconds()}, 230, 230, 60, 60)
		poll()
		elapsed += printer.PollActive
	}
	start := elapsed
	for t0 := time.Duration(0); t0 < job; t0 += printer.PollActive {
		noise := float64((int(t0/printer.PollActive)*37)%51 - 25)
		progress := 1 + 98.9*t0.Seconds()/job.Seconds()
		p.fp.setStatus("PRINTING", &fakeJob{id: 9, progress: progress, remaining: (job - t0).Seconds() + noise, printing: (start + t0).Seconds()},
			230+float64(int(t0/time.Second)%3)-1, 230, 60, 60)
		poll()
	}
	for range 12 {
		p.fp.setStatus("PRINTING", &fakeJob{id: 9, progress: 100, remaining: 0, printing: (start + job).Seconds()}, 170, 0, 50, 0)
		poll()
	}
	p.fp.setStatus("FINISHED", nil, 60, 0, 40, 0)
	for range 12 {
		poll()
	}
	for _, ph := range []string{"heating", "preparing", "printing", "finishing", "finished"} {
		if !phases[ph] {
			t.Errorf("the simulated job never showed %s (saw %v)", ph, phases)
		}
	}
	if changes >= 150 {
		t.Fatalf("ETag changed %d times over %d polls of a 2 h print, want fewer than 150", changes, polls)
	}
	if changes < 100 {
		t.Fatalf("ETag changed only %d times: whole percents are missing", changes)
	}
	t.Logf("%d ETag changes over %d polls", changes, polls)
}

func TestPrintThumbRoute(t *testing.T) {
	p := newPrintFixture(t)
	f := p.viewFixture
	knobPrintOn(t, f)
	postCapsCheckin(t, f, "0.10.0", knobCapsPrint)
	p.printing(t, pngBytes(t, 48, color.RGBA{0x30, 0xC0, 0x50, 255}))
	pr, _ := viewPrint(t, goldenView(t, f))
	ver, _ := pr["thumb_version"].(string)
	if !strings.HasSuffix(ver, "-"+printer.ThumbRecipe) {
		t.Fatalf("thumb_version = %q", ver)
	}
	if rows, _ := pr["thumb_rows"].([]any); len(rows) != 2 || rows[1] != float64(printer.ThumbHeroBottom) {
		t.Fatalf("thumb_rows = %v", pr["thumb_rows"])
	}
	path := "/v1/print/thumb?kind=solid&v=" + ver
	if resp, _ := devReq(t, f.srv, "GET", path, "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", resp.StatusCode)
	}
	read := mintClient(t, f.srv, "dash", "read")
	if resp, _ := devReq(t, f.srv, "GET", path, read.Token, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read client = %d, want 403", resp.StatusCode)
	}
	admin := mintClient(t, f.srv, "ops", "admin")
	if resp, _ := devReq(t, f.srv, "GET", path, admin.Token, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin client = %d, want 200", resp.StatusCode)
	}
	if resp, b := devReq(t, f.srv, "GET", "/v1/print/thumb?kind=cover", f.m.Token, ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("kind=cover = %d %s", resp.StatusCode, b)
	}
	var etag string
	for _, kind := range []string{"solid", "ghost"} {
		for _, tok := range []string{f.m.Token, testToken} {
			resp, b := devReq(t, f.srv, "GET", "/v1/print/thumb?kind="+kind+"&v="+ver, tok, "")
			if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
				t.Fatalf("%s = %d %s", kind, resp.StatusCode, resp.Header.Get("Content-Type"))
			}
			if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=86400, immutable" {
				t.Fatalf("%s at the current v: Cache-Control %q", kind, cc)
			}
			img, err := jpeg.Decode(bytes.NewReader(b))
			if err != nil || img.Bounds().Dx() != printer.ThumbSize || img.Bounds().Dy() != printer.ThumbSize {
				t.Fatalf("%s: %v %v", kind, err, img.Bounds())
			}
			etag = resp.Header.Get("ETag")
			if !strings.Contains(etag, ver) || !strings.Contains(etag, kind) {
				t.Fatalf("%s ETag %q", kind, etag)
			}
		}
	}
	if resp, _ := devReq(t, f.srv, "GET", "/v1/print/thumb?kind=ghost&v=stale", f.m.Token, ""); resp.StatusCode != http.StatusNotFound || resp.Header.Get("Cache-Control") != "" {
		t.Fatalf("stale v = %d, Cache-Control %q, want 404 without caching", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	if resp, _ := devReq(t, f.srv, "GET", "/v1/print/thumb?kind=ghost", f.m.Token, ""); resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("no v = %d, Cache-Control %q, want the current picture, no-cache", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	_, solidBody := devReq(t, f.srv, "GET", "/v1/print/thumb?kind=solid&v="+ver, f.m.Token, "")
	_, ghostBody := devReq(t, f.srv, "GET", "/v1/print/thumb?kind=ghost&v="+ver, f.m.Token, "")
	solidImg, err1 := jpeg.Decode(bytes.NewReader(solidBody))
	ghostImg, err2 := jpeg.Decode(bytes.NewReader(ghostBody))
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	sr, sg, sb, _ := solidImg.At(233, 205).RGBA()
	gr, gg, gb, _ := ghostImg.At(233, 205).RGBA()
	if sg>>8 < 150 || sr>>8 > 90 || gg>>8 > 70 || absDiff(gr>>8, gg>>8) > 3 || absDiff(gb>>8, gg>>8) > 3 {
		t.Fatalf("solid %d,%d,%d ghost %d,%d,%d: want the green model and a dim grey ghost", sr>>8, sg>>8, sb>>8, gr>>8, gg>>8, gb>>8)
	}
	if resp, _ := npDo(t, f.srv, "GET", "/v1/print/thumb?kind=ghost", f.m.Token, nil, map[string]string{"If-None-Match": etag}); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match = %d, want 304", resp.StatusCode)
	}
	if got := p.fp.hit(printThumbRef); got != 1 {
		t.Fatalf("printer thumbnail read %d times, want once", got)
	}

	p.fp.setJob(0, "", "", nil)
	p.fp.setStatus("IDLE", nil, 30, 0, 30, 0)
	p.step(t, 5*time.Second)
	p.fp.setJob(413, "PRINTING", "lid_0.4n_0.2mm_PLA_COREONE_1h0m.bgcode", pngBytes(t, 48, color.RGBA{0x20, 0x40, 0xD0, 255}))
	p.fp.setStatus("PRINTING", &fakeJob{id: 413, progress: 3, remaining: 3500, printing: 100}, 215, 215, 60, 60)
	p.step(t, 5*time.Second)
	pr, _ = viewPrint(t, goldenView(t, f))
	next, _ := pr["thumb_version"].(string)
	if next == "" || next == ver {
		t.Fatalf("new job thumb_version = %q, was %q", next, ver)
	}
	if resp, _ := devReq(t, f.srv, "GET", "/v1/print/thumb?kind=solid&v="+ver, f.m.Token, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the old job's v = %d, want 404 so a solid and ghost can't mix", resp.StatusCode)
	}

	p.fp.setJob(0, "", "", nil)
	p.fp.setStatus("IDLE", nil, 30, 0, 30, 0)
	p.step(t, 5*time.Second)
	p.fp.setJob(414, "PRINTING", "plain.gcode", nil)
	p.fp.setStatus("PRINTING", &fakeJob{id: 414, progress: 3, remaining: 3500, printing: 100}, 215, 215, 60, 60)
	p.step(t, 5*time.Second)
	pr, ok := viewPrint(t, goldenView(t, f))
	if !ok || pr["name"] != "plain" {
		t.Fatalf("job without a thumbnail: %v", pr)
	}
	if v, has := pr["thumb_version"]; has {
		t.Fatalf("job without a thumbnail has thumb_version %v", v)
	}
	if resp, _ := devReq(t, f.srv, "GET", "/v1/print/thumb?kind=solid", f.m.Token, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no thumbnail = %d, want 404", resp.StatusCode)
	}
}

func TestKnobViewPrintSkipsACorruptThumbnail(t *testing.T) {
	p := newPrintFixture(t)
	knobPrintOn(t, p.viewFixture)
	postCapsCheckin(t, p.viewFixture, "0.10.0", knobCapsPrint)
	good := pngBytes(t, 48, color.RGBA{0x30, 0xC0, 0x50, 255})
	p.printing(t, good[:len(good)-24])
	pr, ok := viewPrint(t, goldenView(t, p.viewFixture))
	if !ok || pr["name"] != "bracket_v3" {
		t.Fatalf("block = %v", pr)
	}
	if v, has := pr["thumb_version"]; has {
		t.Fatalf("a truncated PNG got thumb_version %v; K2 would not fall back", v)
	}
	resp, _ := devReq(t, p.srv, "GET", "/v1/print/thumb?kind=solid", p.m.Token, "")
	if resp.StatusCode != http.StatusNotFound || resp.Header.Get("ETag") != "" || strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("thumb = %d ETag %q Cache-Control %q", resp.StatusCode, resp.Header.Get("ETag"), resp.Header.Get("Cache-Control"))
	}
}

func TestCapsKnobViewDropsPrintAfterPomoToFitViewBytes(t *testing.T) {
	p := newPrintFixture(t)
	f := p.viewFixture
	f.app.zone = time.UTC
	f.app.dayClock = f.clk.Now
	pages := []string{"bot", "day", "pomodoro", "print"}
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": pages, "features": []string{}})
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"day","on":true},{"id":"pomodoro","on":true},{"id":"print","on":true}],"home":"bot"}`)
	f.eng.Start(pomodoro.PhaseFocus)
	p.printing(t, nil)
	full := goldenView(t, f)
	limited := func(n int) map[string]json.RawMessage {
		t.Helper()
		plantCaps(t, f, &devices.Caps{View: []int{1, 1}, Pages: pages, Features: []string{},
			Limits: &devices.CapsLimits{ViewBytes: n}})
		body := goldenView(t, f)
		if len(body) > n {
			t.Fatalf("view is %d B, over %d", len(body), n)
		}
		return viewKeys(t, body)
	}
	if _, ok := viewKeys(t, full)["day"]; !ok {
		t.Fatalf("the full view lacks day: %s", full)
	}
	keys := limited(len(full) - 1)
	if _, ok := keys["pomo"]; ok {
		t.Fatalf("pomo kept in an oversized view: %v", keys)
	}
	if _, ok := keys["print"]; !ok {
		t.Fatalf("print dropped though dropping pomo was enough: %v", keys)
	}
	noPomo := len(full) - len(`,"pomo":`) - len(viewKeys(t, full)["pomo"])
	keys = limited(noPomo - len(`"print":{}`))
	if _, ok := keys["print"]; ok {
		t.Fatalf("print kept in a view too small for it: %v", keys)
	}
	if _, ok := keys["mood"]; !ok {
		t.Fatalf("mood dropped: %v", keys)
	}
	if _, ok := keys["day"]; !ok {
		t.Fatalf("day dropped before print: %v", keys)
	}
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
