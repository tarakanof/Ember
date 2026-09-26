package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

// putClockOverride pins url the way the menu does: PUT /v1/device/config.
func putClockOverride(a *App, url string) error {
	body, _ := json.Marshal(map[string]string{"base_url": url})
	w := putDeviceConfig(a, string(body))
	if w.Code != http.StatusOK {
		return fmt.Errorf("PUT /v1/device/config: %d %s", w.Code, w.Body)
	}
	return nil
}

func putDeviceConfig(a *App, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.handleDeviceConfigPut(w, httptest.NewRequest("PUT", "/v1/device/config", strings.NewReader(body)))
	return w
}

// getDeviceConfig is GET /v1/device/config's body.
func getDeviceConfig(t *testing.T, a *App) (url, source string) {
	t.Helper()
	w := httptest.NewRecorder()
	a.handleDeviceConfigGet(w, httptest.NewRequest("GET", "/v1/device/config", nil))
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("GET body %s: %v", w.Body, err)
	}
	return got["base_url"], got["source"]
}

func storedOverride(t *testing.T, a *App) string {
	t.Helper()
	v, _, err := a.store.GetSetting(deviceBaseURLKey)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestClockURL_Precedence(t *testing.T) {
	cases := []struct {
		name                           string
		baseline, override, discovered string
		wantURL, wantSource            string
	}{
		{"nothing", "", "", "", "", "none"},
		{"baseline", "http://b", "", "", "http://b", "config"},
		{"override beats baseline", "http://b", "http://o", "", "http://o", "store"},
		{"override without baseline", "", "http://o", "", "http://o", "store"},
		{"swap beats override", "http://b", "http://o", "http://d", "http://d", "discovered"},
		{"swap beats baseline", "http://b", "", "http://d", "http://d", "discovered"},
		{"swap equal to baseline is still discovered", "http://b", "", "http://b", "http://b", "discovered"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var cfg Config
			cfg.AWTRIX.HTTPBaseURL, cfg.AWTRIX.clockOverride, cfg.AWTRIX.clockDiscovered = c.baseline, c.override, c.discovered
			url, src := cfg.clockURL()
			if url != c.wantURL || src != c.wantSource {
				t.Fatalf("clockURL() = %q, %q; want %q, %q", url, src, c.wantURL, c.wantSource)
			}
		})
	}
}

// The override persists; a discovery swap is in memory only, so a restart
// comes back on the override.
func TestClockOverride_SurvivesRestartSwapDoesNot(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	a := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if err := a.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	if err := putClockOverride(a, "http://10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if !a.swapDiscoveredClock("http://10.0.0.5", "http://10.0.0.6:80") {
		t.Fatal("swap refused")
	}
	if got := storedOverride(t, a); got != "http://10.0.0.5" {
		t.Fatalf("store after swap = %q, want the override untouched", got)
	}
	if err := a.store.Close(); err != nil {
		t.Fatal(err)
	}

	b := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if err := b.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.store.Close() })
	b.settings.reapply()
	if url, src := getDeviceConfig(t, b); url != "http://10.0.0.5" || src != "store" {
		t.Fatalf("after restart: %q (%s), want the override http://10.0.0.5 (store)", url, src)
	}
}

// A store written before the clock URL joined the overlay holds the raw URL;
// it must still apply, and a PUT keeps writing that format.
func TestClockOverride_RawStoreFormat(t *testing.T) {
	a := newTestAppWithStore(t)
	if err := a.store.PutSetting(deviceBaseURLKey, "http://10.0.0.8"); err != nil {
		t.Fatal(err)
	}
	a.settings.reapply()
	if url, src := getDeviceConfig(t, a); url != "http://10.0.0.8" || src != "store" {
		t.Fatalf("legacy blob: %q (%s)", url, src)
	}
	if err := putClockOverride(a, "https://clock.local"); err != nil {
		t.Fatal(err)
	}
	if got := storedOverride(t, a); got != "https://clock.local" {
		t.Fatalf("stored = %q, want the raw URL", got)
	}
}

// An invalid stored value (hand-edited DB) is ignored, leaving the baseline.
func TestClockOverride_InvalidStoredValueIgnored(t *testing.T) {
	a := newTestAppWithStore(t)
	if err := a.store.PutSetting(deviceBaseURLKey, "file:///etc/passwd"); err != nil {
		t.Fatal(err)
	}
	a.settings.reapply()
	if url, src := getDeviceConfig(t, a); url != defaultDeviceBaseURL || src != "config" {
		t.Fatalf("got %q (%s), want the baseline", url, src)
	}
}

func TestDeviceConfigPut_MergeSemantics(t *testing.T) {
	a := newTestAppWithStore(t)

	// No override yet: {} changes nothing.
	if w := putDeviceConfig(a, `{}`); w.Code != http.StatusOK {
		t.Fatalf("{} without override: %d %s", w.Code, w.Body)
	}
	if url, src := getDeviceConfig(t, a); url != defaultDeviceBaseURL || src != "config" {
		t.Fatalf("after {}: %q (%s)", url, src)
	}

	if err := putClockOverride(a, "http://10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{"base_url":null}`, `{"other":1}`} {
		w := putDeviceConfig(a, body)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"base_url":"http://10.0.0.5"`) {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	// An explicit empty or bad URL is still a 400 and changes nothing.
	for _, body := range []string{`{"base_url":""}`, `{"base_url":"ftp://x"}`, `{"base_url":5}`, `"http://x"`, `[]`} {
		if w := putDeviceConfig(a, body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, w.Code)
		}
	}
	if url, src := getDeviceConfig(t, a); url != "http://10.0.0.5" || src != "store" {
		t.Fatalf("after rejected PUTs: %q (%s)", url, src)
	}
	if got := storedOverride(t, a); got != "http://10.0.0.5" {
		t.Fatalf("stored = %q", got)
	}
}

// A PUT naming base_url re-pins it over a discovery swap, even when it is the
// very override discovery swapped away from; {} leaves the swap alone.
func TestDeviceConfigPut_RepinsOverDiscoverySwap(t *testing.T) {
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, "http://10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	a.swapDiscoveredClock("http://10.0.0.5", "http://10.0.0.9:80")
	if _, src := getDeviceConfig(t, a); src != "discovered" {
		t.Fatalf("after swap: source %s", src)
	}
	putDeviceConfig(a, `{}`)
	if url, src := getDeviceConfig(t, a); url != "http://10.0.0.9:80" || src != "discovered" {
		t.Fatalf("{} disturbed the swap: %q (%s)", url, src)
	}
	if err := putClockOverride(a, "http://10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if url, src := getDeviceConfig(t, a); url != "http://10.0.0.5" || src != "store" {
		t.Fatalf("re-pin: %q (%s)", url, src)
	}
}

// A swap computed against a URL the menu has since replaced must not land.
func TestSwapDiscoveredClock_LosesToConcurrentPin(t *testing.T) {
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, "http://10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if a.swapDiscoveredClock(defaultDeviceBaseURL, "http://10.0.0.9") {
		t.Fatal("swap from a URL that is no longer effective landed")
	}
	if url, src := getDeviceConfig(t, a); url != "http://10.0.0.5" || src != "store" {
		t.Fatalf("got %q (%s)", url, src)
	}
}

// A reload that changes the file URL re-pins: a swap is dropped, and a store
// override still wins over the new baseline.
func TestAdminReload_FileURLChangeDropsSwapOverrideWins(t *testing.T) {
	app, path := newAppForReload(t, `{"awtrix":{"http_base_url":"http://1.2.3.4"}}`)
	if err := app.ensureStore(filepath.Join(t.TempDir(), "s.db")); err != nil {
		t.Fatal(err)
	}
	if err := putClockOverride(app, "http://10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	app.swapDiscoveredClock("http://10.0.0.1", "http://5.6.7.8")

	postReload(t, app, path, `{"awtrix":{"http_base_url":"http://9.9.9.9"}}`)

	if url, src := getDeviceConfig(t, app); url != "http://10.0.0.1" || src != "store" {
		t.Fatalf("after file URL change: %q (%s), want the override", url, src)
	}
	if got := app.cfg.Load().AWTRIX.HTTPBaseURL; got != "http://9.9.9.9" {
		t.Fatalf("baseline = %q", got)
	}
}

// Without a store the override lives in memory; a reload must keep it.
func TestAdminReload_KeepsInMemoryOverride(t *testing.T) {
	app, path := newAppForReload(t, `{"awtrix":{"http_base_url":"http://1.2.3.4"}}`)
	if err := putClockOverride(app, "http://10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	postReload(t, app, path, `{"awtrix":{"http_base_url":"http://9.9.9.9"}}`)
	if url, src := getDeviceConfig(t, app); url != "http://10.0.0.1" || src != "store" {
		t.Fatalf("got %q (%s)", url, src)
	}
}

// changed_fields names awtrix.http_base_url exactly when the file changed it,
// whatever the running URL is.
func TestAdminReload_ChangedFieldsTrackFileURL(t *testing.T) {
	app, path := newAppForReload(t, `{"awtrix":{"http_base_url":"http://1.2.3.4"}}`)
	app.swapDiscoveredClock("http://1.2.3.4", "http://5.6.7.8")
	for _, c := range []struct {
		file string
		want bool
	}{{"http://1.2.3.4", false}, {"http://5.6.7.8", true}} {
		if err := os.WriteFile(path, []byte(`{"awtrix":{"http_base_url":"`+c.file+`"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/admin/reload", nil)
		r.Header.Set("Authorization", "Bearer tok")
		app.routes().ServeHTTP(w, r)
		var body struct {
			Changed []string `json:"changed_fields"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
			t.Fatalf("reload: %d %s", w.Code, w.Body)
		}
		if got := slices.Contains(body.Changed, "awtrix.http_base_url"); got != c.want {
			t.Fatalf("file %s: changed_fields %v, want http_base_url listed=%v", c.file, body.Changed, c.want)
		}
	}
}

// A menu PUT racing the watch loop's discovery swap: no data race, the store
// only ever holds what a PUT wrote, and every GET is a consistent pair.
func TestDeviceConfigPut_ConcurrentWithDiscoverySwap(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()
	a := newTestAppWithStore(t)
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}
	pins := []string{"http://127.0.0.1:9", "http://127.0.0.1:7"} // both dead
	if err := putClockOverride(a, pins[0]); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := putClockOverride(a, pins[i%2]); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for i := 0; i < 10; i++ {
			a.rediscoverClock(ctx)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			url, src := a.cfg.Load().clockURL()
			switch src {
			case "store":
				if !slices.Contains(pins, url) {
					t.Errorf("source store with %q", url)
				}
			case "discovered":
				if url != clock.URL {
					t.Errorf("source discovered with %q", url)
				}
			default:
				t.Errorf("unexpected source %q (%q)", src, url)
			}
		}
	}()
	wg.Wait()

	if got := storedOverride(t, a); got != pins[1] {
		t.Fatalf("store = %q, want the last PUT %q", got, pins[1])
	}
	a.rediscoverClock(context.Background())
	if url, src := getDeviceConfig(t, a); url != clock.URL || src != "discovered" {
		t.Fatalf("settled: %q (%s), want the live clock", url, src)
	}
}

// clockURLBaselineFiles may read AWTRIXConfig.HTTPBaseURL: it means the file
// baseline, and everything else must ask Config.clockURL for the URL to use.
var clockURLBaselineFiles = []string{"config.go", "printconfig.go", "admin.go", "clock_url.go"}

func TestHTTPBaseURLOnlyReadAsBaseline(t *testing.T) {
	fset, files, info := typeCheckPackage(t)
	for _, f := range files {
		name := filepath.Base(fset.Position(f.Pos()).Filename)
		if slices.Contains(clockURLBaselineFiles, name) {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HTTPBaseURL" {
				return true
			}
			if v, ok := info.Uses[sel.Sel].(*types.Var); ok && v.IsField() {
				t.Errorf("%s: AWTRIX.HTTPBaseURL is the file baseline; use Config.clockURL()/effectiveClockURL()", fset.Position(sel.Pos()))
			}
			return true
		})
	}
}
