package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/berry"
)

func TestBuildBootCallbackURL(t *testing.T) {
	cases := []struct {
		ip, addr, want string
	}{
		{"192.168.0.2", ":3627", "http://192.168.0.2:3627/hooks/awtrix/boot"},
		{"10.0.0.5", "0.0.0.0:3627", "http://10.0.0.5:3627/hooks/awtrix/boot"},
		{"", ":3627", ""},
		{"192.168.0.2", "", ""},
		{"192.168.0.2", "junk", ""},
	}
	for _, c := range cases {
		if got := buildBootCallbackURL(c.ip, c.addr); got != c.want {
			t.Errorf("buildBootCallbackURL(%q,%q)=%q want %q", c.ip, c.addr, got, c.want)
		}
	}
}

func TestBootHookRepublishesWithoutAuth(t *testing.T) {
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.Auth.StatusToken = "s3cret" })
	srv := httptest.NewServer(a.routes())
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL+"/hooks/awtrix/boot", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d want 204", resp.StatusCode)
	}
	select {
	case cmd := <-a.coord.cmds:
		if cmd.kind != cmdRepublish {
			t.Fatalf("queued %v want cmdRepublish", cmd.kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("boot hook queued no republish")
	}
}

type fakeScriptDevice struct {
	mu       sync.Mutex
	source   string
	present  bool
	puts     []string
	deletes  []string
	putReply string
}

func (f *fakeScriptDevice) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		scriptPath := "/api/v1/apps/script/" + berry.BootPingName
		appPath := "/api/v1/apps/" + berry.BootPingName
		switch {
		case r.Method == http.MethodGet && r.URL.Path == scriptPath:
			if !f.present {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, f.source)
		case r.Method == http.MethodPut && r.URL.Path == scriptPath:
			b, _ := io.ReadAll(r.Body)
			f.puts = append(f.puts, string(b))
			f.source, f.present = string(b), true
			w.Header().Set("Content-Type", "application/json")
			if f.putReply != "" {
				_, _ = io.WriteString(w, f.putReply)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"name":"`+berry.BootPingName+`","error":null}`)
		case r.Method == http.MethodDelete && r.URL.Path == appPath:
			f.deletes = append(f.deletes, r.URL.Path)
			f.source, f.present = "", false
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
}

func (f *fakeScriptDevice) snapshot() (puts, deletes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.puts...), append([]string(nil), f.deletes...)
}

func bootPingTestApp(t *testing.T, deviceURL string, on bool) *App {
	t.Helper()
	a := newTestAppWithStore(t)
	a.updateConfig(func(c *Config) {
		c.AWTRIX.HTTPBaseURL = deviceURL
		c.AWTRIX.BootPing = on
	})
	return a
}

func TestEnsureBootPingScriptInstallsWithCallbackURL(t *testing.T) {
	dev := &fakeScriptDevice{}
	srv := dev.server(t)
	defer srv.Close()
	a := bootPingTestApp(t, srv.URL, true)

	a.ensureBootPingScript(context.Background())

	puts, _ := dev.snapshot()
	if len(puts) != 1 {
		t.Fatalf("puts=%d want 1", len(puts))
	}
	want := a.expectedBootCallback()
	if want == "" {
		t.Fatal("test env cannot derive a callback URL")
	}
	if !strings.Contains(puts[0], `default="`+want+`"`) {
		t.Fatalf("installed script does not carry %q:\n%s", want, puts[0])
	}
	if strings.Contains(puts[0], "__EMBER_BOOT_URL__") {
		t.Fatalf("placeholder shipped to the device:\n%s", puts[0])
	}
}

func TestEnsureBootPingScriptSkipsUnchangedSource(t *testing.T) {
	dev := &fakeScriptDevice{}
	srv := dev.server(t)
	defer srv.Close()
	a := bootPingTestApp(t, srv.URL, true)

	a.ensureBootPingScript(context.Background())
	a.ensureBootPingScript(context.Background())

	if puts, _ := dev.snapshot(); len(puts) != 1 {
		t.Fatalf("puts=%d want 1 (second run should be a no-op)", len(puts))
	}
}

func TestEnsureBootPingScriptReinstallsOnCallbackChange(t *testing.T) {
	dev := &fakeScriptDevice{present: true, source: berry.BootPingSource("http://10.9.9.9:1/hooks/awtrix/boot")}
	srv := dev.server(t)
	defer srv.Close()
	a := bootPingTestApp(t, srv.URL, true)

	a.ensureBootPingScript(context.Background())

	puts, _ := dev.snapshot()
	if len(puts) != 1 {
		t.Fatalf("puts=%d want 1 (stale callback must be replaced)", len(puts))
	}
	if strings.Contains(puts[0], "10.9.9.9") {
		t.Fatalf("stale callback survived:\n%s", puts[0])
	}
}

func TestEnsureBootPingScriptRemovesWhenToggledOff(t *testing.T) {
	dev := &fakeScriptDevice{present: true, source: berry.BootPingSource("http://e/hooks/awtrix/boot")}
	srv := dev.server(t)
	defer srv.Close()
	a := bootPingTestApp(t, srv.URL, false)

	a.ensureBootPingScript(context.Background())

	puts, deletes := dev.snapshot()
	if len(puts) != 0 {
		t.Fatalf("puts=%d want 0 while disabled", len(puts))
	}
	if len(deletes) != 1 {
		t.Fatalf("deletes=%d want 1", len(deletes))
	}
}

func TestEnsureBootPingScriptNoopWhenOffAndAbsent(t *testing.T) {
	dev := &fakeScriptDevice{}
	srv := dev.server(t)
	defer srv.Close()
	a := bootPingTestApp(t, srv.URL, false)

	a.ensureBootPingScript(context.Background())

	if puts, deletes := dev.snapshot(); len(puts) != 0 || len(deletes) != 0 {
		t.Fatalf("puts=%d deletes=%d want 0/0", len(puts), len(deletes))
	}
}

func TestPutScriptSurfacesCompileError(t *testing.T) {
	dev := &fakeScriptDevice{putReply: `{"ok":true,"name":"x","error":{"message":"syntax_error","line":12}}`}
	srv := dev.server(t)
	defer srv.Close()
	a := bootPingTestApp(t, srv.URL, true)

	err := a.putScript(context.Background(), berry.BootPingName, "class X end")
	if err == nil {
		t.Fatal("a 200-with-error reply must be an error")
	}
	if !strings.Contains(err.Error(), "syntax_error") {
		t.Fatalf("err=%v want the compiler message", err)
	}
}

func TestEnsureBootPingScriptSurvivesUnreachableClock(t *testing.T) {
	a := bootPingTestApp(t, "http://127.0.0.1:9", true)
	a.ensureBootPingScript(context.Background())
}
