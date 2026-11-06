package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/printer"
)

const printTestPassword = "pw-sentinel-c0ffee42"

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newPrintApp(t *testing.T, logs *lockedBuffer) (*App, *httptest.Server) {
	t.Helper()
	cfg := stubClockConfig(t)
	cfg.applyDefaults()
	cfg.Auth.StatusToken = testToken
	cfg.RateLimit.Disabled = true
	app := guardApp(t, NewApp(cfg, &recordingPublisher{}, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	if err := app.ensureStore(filepath.Join(t.TempDir(), "s.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.store.Close() })
	if err := app.loadRegistries(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	ownIconJobs(t, app)
	return app, srv
}

func fakePrusaLink(t *testing.T, acceptAuth bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptAuth || !strings.HasPrefix(r.Header.Get("Authorization"), `Digest username="maker"`) {
			w.Header().Set("WWW-Authenticate", `Digest realm="Printer API", nonce="0123456789abcdef", stale=false`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/status":
			_, _ = w.Write([]byte(`{"job":{"id":412,"progress":42.0,"time_remaining":4680,"time_printing":3360},"printer":{"state":"PRINTING","temp_bed":85.1,"target_bed":85,"temp_nozzle":240.2,"target_nozzle":240}}`))
		case "/api/v1/job":
			_, _ = w.Write([]byte(`{"id":412,"state":"PRINTING","file":{"refs":{"thumbnail":"/thumb/l/usb/BRACKE~1.BGC"},"name":"BRACKE~1.BGC","display_name":"bracket_v3_0.4n_0.2mm_PETG_COREONE_2h14m.bgcode"}}`))
		case "/api/version":
			_, _ = w.Write([]byte(`{"hostname":"prusa-core-one","firmware":"8.0.0"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPrintStateNeedsOwnerOrReadToken(t *testing.T) {
	_, srv := newPrintApp(t, &lockedBuffer{})
	if resp, _ := devReq(t, srv, "GET", "/v1/print/state", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", resp.StatusCode)
	}
	read := mintClient(t, srv, "dash", "read")
	ingest := mintClient(t, srv, "ci", "ingest")
	if resp, _ := devReq(t, srv, "GET", "/v1/print/state", ingest.Token, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ingest token = %d, want 403", resp.StatusCode)
	}
	for _, tok := range []string{testToken, read.Token} {
		resp, b := devReq(t, srv, "GET", "/v1/print/state", tok, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("state = %d %s", resp.StatusCode, b)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil || got["phase"] != "off" || got["enabled"] != false {
			t.Fatalf("state = %s", b)
		}
	}
	if resp, _ := devReq(t, srv, "PUT", "/v1/print/config", read.Token, `{"enabled":true}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read token PUT = %d, want 403", resp.StatusCode)
	}
}

func TestPrintConfigMergesValidatesAndPersists(t *testing.T) {
	app, srv := newPrintApp(t, &lockedBuffer{})
	resp, b := devReq(t, srv, "GET", "/v1/print/config", testToken, "")
	want := `{"enabled":false,"url":"auto","name":"CORE One","clock_app":true,"notify_done":true,"notify_attention":true,"done_hold_min":30,"show":"printing"}`
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(b)) != want {
		t.Fatalf("GET = %d %s", resp.StatusCode, b)
	}
	resp, b = devReq(t, srv, "PUT", "/v1/print/config", testToken, `{"enabled":true,"url":"http://192.168.0.50/","notify_done":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, b)
	}
	got := app.cfg.Load().Printer
	if !got.Enabled || got.URL != "http://192.168.0.50" || got.NotifyDoneEnabled() || !got.NotifyAttentionEnabled() || got.DoneHoldMin != 30 {
		t.Fatalf("config = %+v", got)
	}
	for _, step := range []struct{ body, want string }{
		{`{"show":"on"}`, printer.ShowOn},
		{`{"enabled":true}`, printer.ShowOn},
		{`{"show":"always"}`, printer.ShowPrinting},
		{`{"show":"on"}`, printer.ShowOn},
		{`{"show":""}`, printer.ShowPrinting},
	} {
		if resp, b := devReq(t, srv, "PUT", "/v1/print/config", testToken, step.body); resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", step.body, resp.StatusCode, b)
		}
		if got := app.cfg.Load().Printer.Show; got != step.want {
			t.Errorf("after PUT %s show = %q, want %q", step.body, got, step.want)
		}
	}
	for _, bad := range []string{`{"url":"http://maker:pw@printer"}`, `{"url":"printer"}`, `{"done_hold_min":-1}`, `{"done_hold_min":5000}`} {
		if resp, b := devReq(t, srv, "PUT", "/v1/print/config", testToken, bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PUT %s = %d %s, want 400", bad, resp.StatusCode, b)
		}
	}
	blob, ok, err := app.store.GetSetting(printerSettingsKey)
	if err != nil || !ok || !strings.Contains(blob, `"url":"http://192.168.0.50"`) {
		t.Fatalf("persisted = %q %v %v", blob, ok, err)
	}
	cur := *app.cfg.Load()
	cur.Printer = printer.Config{}
	cur.Printer.ApplyDefaults()
	app.cfg.Store(&cur)
	app.reapplySettings()
	if got := app.cfg.Load().Printer; !got.Enabled || got.URL != "http://192.168.0.50" {
		t.Fatalf("after reapply = %+v", got)
	}
}

func TestPrintStateGolden(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ev := printer.Event{ID: 1793880000000, Kind: printer.EventStarted, At: now.Add(-56 * time.Minute), JobID: 412}
	printing := printer.Snapshot{
		Enabled: true, Phase: printer.PhasePrinting, PrinterState: "PRINTING", JobID: 412, Progress: 42,
		EndsAt: now.Add(78 * time.Minute), StartedAt: now.Add(-56 * time.Minute), ChangeAt: now.Add(16 * time.Minute),
		Nozzle: 240.2, NozzleTarget: 240, Bed: 85.1, BedTarget: 85, Name: "bracket_v3", Material: "PETG",
		ThumbVersion: "1a2b3c4d5e6f", Event: &ev, Events: []printer.Event{ev}, UpdatedAt: now,
		Printer: printer.Info{Name: "CORE One", URL: "http://192.168.0.50", Host: "prusa-core-one.local",
			Hostname: "prusa-core-one", Firmware: "8.0.0", Discovered: true},
	}
	compareGoldenJSON(t, "state_printing", printState(printing))
	offline := printer.Snapshot{Enabled: true, Phase: printer.PhaseOffline, LastError: printer.ErrCodeWrongPassword,
		Printer: printer.Info{Name: "CORE One", URL: "http://192.168.0.50", Host: "192.168.0.50"}}
	compareGoldenJSON(t, "state_wrong_password", printState(offline))
}

func compareGoldenJSON(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, filepath.Join("testdata", "print", name+".json"), append(got, '\n'))
}

func TestPrinterPasswordAppearsNowhere(t *testing.T) {
	for _, accept := range []bool{true, false} {
		logs := &lockedBuffer{}
		app, srv := newPrintApp(t, logs)
		pl := fakePrusaLink(t, accept)
		if resp, b := devReq(t, srv, "PUT", "/v1/print/config", testToken, `{"enabled":true,"url":"`+pl.URL+`"}`); resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT = %d %s", resp.StatusCode, b)
		}
		app.configurePrinter(printer.Credentials{User: "maker", Password: printTestPassword})
		for range 3 {
			app.printer.Step(context.Background())
		}
		_, state := devReq(t, srv, "GET", "/v1/print/state", testToken, "")
		_, cfg := devReq(t, srv, "GET", "/v1/print/config", testToken, "")
		_, doctor := devReq(t, srv, "GET", "/admin/doctor", testToken, "")
		var st printStateDTO
		if err := json.Unmarshal(state, &st); err != nil {
			t.Fatal(err)
		}
		if accept && (st.Phase != printer.PhasePrinting || st.Name != "bracket_v3" || st.Printer.Firmware != "8.0.0") {
			t.Fatalf("state = %s", state)
		}
		if !accept && st.LastError != string(printer.ErrCodeWrongPassword) {
			t.Fatalf("state = %s", state)
		}
		if !strings.Contains(string(doctor), `"printer"`) {
			t.Fatalf("doctor has no printer check: %s", doctor)
		}
		for name, text := range map[string]string{"logs": logs.String(), "state": string(state), "config": string(cfg), "doctor": string(doctor)} {
			if strings.Contains(text, printTestPassword) {
				t.Errorf("accept=%v: %s contains the password:\n%s", accept, name, text)
			}
		}
		if !strings.Contains(logs.String(), "password_set=true") {
			t.Errorf("startup log has no redacted credentials:\n%s", logs.String())
		}
	}
}

func TestPrinterDoctorCheck(t *testing.T) {
	app, _ := newPrintApp(t, &lockedBuffer{})
	if c := checkPrinter(app); c.Status != StatusOK || !strings.Contains(c.Detail, "disabled") {
		t.Fatalf("disabled = %+v", c)
	}
	app.updateConfig(func(c *Config) { c.Printer.Enabled = true; c.Printer.URL = "http://127.0.0.1:1" })
	app.printer.Step(context.Background())
	if c := checkPrinter(app); c.Status != StatusWarn || !strings.Contains(c.Detail, printer.PasswordEnv) {
		t.Fatalf("no password = %+v", c)
	}
}

func TestNoPrinterPasswordInFixtures(t *testing.T) {
	secrets := []string{printTestPassword, "pw-sentinel-7f3a9c"}
	if real := os.Getenv(printer.PasswordEnv); real != "" {
		secrets = append(secrets, real)
	}
	var files []string
	for _, root := range []string{"testdata", "../../internal/printer", "../../docs", "../../deploy"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 10 {
		t.Fatalf("scanned only %d files", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, s := range secrets {
			if strings.Contains(string(b), s) {
				t.Errorf("%s contains printer password #%d", f, i)
			}
		}
	}
}
