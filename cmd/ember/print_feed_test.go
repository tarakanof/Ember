package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/printer"
)

type printFeedStub struct {
	mu       sync.Mutex
	progress float64
	nozzle   float64
}

func (s *printFeedStub) set(progress, nozzle float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress, s.nozzle = progress, nozzle
}

func (s *printFeedStub) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), `Digest username="maker"`) {
			w.Header().Set("WWW-Authenticate", `Digest realm="Printer API", nonce="0123456789abcdef", stale=false`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		s.mu.Lock()
		progress, nozzle := s.progress, s.nozzle
		s.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/status":
			fmt.Fprintf(w, `{"job":{"id":412,"progress":%g,"time_remaining":4680,"time_printing":3360},"printer":{"state":"PRINTING","temp_bed":85.1,"target_bed":85,"temp_nozzle":%g,"target_nozzle":240}}`, progress, nozzle)
		case "/api/v1/job":
			_, _ = w.Write([]byte(`{"id":412,"state":"PRINTING","file":{"name":"BRACKE~1.BGC","display_name":"bracket_v3.bgcode"}}`))
		case "/api/version":
			_, _ = w.Write([]byte(`{"hostname":"prusa-core-one","firmware":"8.0.0"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func printFeedReq(t *testing.T, srv *httptest.Server, query, etag string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("GET", srv.URL+"/v1/print/state"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp, resp.Header.Get("ETag")
}

func newPrintFeedApp(t *testing.T) (*App, *httptest.Server, *printFeedStub) {
	t.Helper()
	app, srv := newPrintApp(t, &lockedBuffer{})
	stub := &printFeedStub{progress: 42, nozzle: 240.2}
	pl := stub.serve(t)
	if resp, b := devReq(t, srv, "PUT", "/v1/print/config", testToken, `{"enabled":true,"url":"`+pl.URL+`"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, b)
	}
	app.configurePrinter(printer.Credentials{User: "maker", Password: printTestPassword})
	for range 3 {
		app.printer.Step(context.Background())
	}
	return app, srv, stub
}

func TestPrintStateETagIgnoresTemperatures(t *testing.T) {
	app, srv, stub := newPrintFeedApp(t)
	resp, etag := printFeedReq(t, srv, "", "")
	if resp.StatusCode != http.StatusOK || etag == "" || resp.Header.Get(knobViewWaitHeader) == "" {
		t.Fatalf("first GET = %d etag=%q wait=%q", resp.StatusCode, etag, resp.Header.Get(knobViewWaitHeader))
	}
	stub.set(42.6, 236.4)
	app.printer.Step(context.Background())
	if resp, again := printFeedReq(t, srv, "", etag); resp.StatusCode != http.StatusNotModified || again != etag {
		t.Fatalf("temperature and sub-percent move = %d etag %q→%q, want 304", resp.StatusCode, etag, again)
	}
	stub.set(43, 236.4)
	app.printer.Step(context.Background())
	if resp, moved := printFeedReq(t, srv, "", etag); resp.StatusCode != http.StatusOK || moved == etag {
		t.Fatalf("whole percent = %d etag %q→%q, want 200 and a new ETag", resp.StatusCode, etag, moved)
	}
}

func TestPrintStateLongPollWakesOnProgress(t *testing.T) {
	app, srv, stub := newPrintFeedApp(t)
	_, etag := printFeedReq(t, srv, "", "")
	type result struct {
		code int
		took time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, _ := printFeedReq(t, srv, "?wait=20", etag)
		done <- result{resp.StatusCode, time.Since(start)}
	}()
	select {
	case r := <-done:
		t.Fatalf("long-poll answered %d before any change", r.code)
	case <-time.After(300 * time.Millisecond):
	}
	stub.set(44, 240)
	app.printer.Step(context.Background())
	select {
	case r := <-done:
		if r.code != http.StatusOK || r.took > 5*time.Second {
			t.Fatalf("long-poll = %d after %v, want 200 soon after the change", r.code, r.took)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("long-poll did not wake on a progress change")
	}
}

func TestPrintStateETagFields(t *testing.T) {
	ev := printEventDTO{ID: 7, Kind: printer.EventFinished, At: 100}
	pct := func(v int) *int { return &v }
	deg := func(v int) *int { return &v }
	base := printStateDTO{Enabled: true, Phase: printer.PhasePrinting, Progress: pct(42), JobID: 412,
		Nozzle: deg(240), Bed: deg(85), Name: "bracket", Printer: printInfoDTO{Name: "CORE One", Firmware: "8.0.0"}}
	same := base
	same.Nozzle, same.Bed, same.UpdatedAt = deg(231), deg(60), new(int64)
	if base.etag() != same.etag() {
		t.Fatal("temperatures or updated_at moved the ETag")
	}
	for name, edit := range map[string]func(*printStateDTO){
		"phase":      func(d *printStateDTO) { d.Phase = printer.PhasePaused },
		"progress":   func(d *printStateDTO) { d.Progress = pct(43) },
		"last_error": func(d *printStateDTO) { d.LastError = "unreachable" },
		"event":      func(d *printStateDTO) { d.Event = &ev },
		"enabled":    func(d *printStateDTO) { d.Enabled = false },
		"firmware":   func(d *printStateDTO) { d.Printer.Firmware = "8.1.0" },
	} {
		d := base
		edit(&d)
		if d.etag() == base.etag() {
			t.Errorf("%s change kept the ETag", name)
		}
	}
}
