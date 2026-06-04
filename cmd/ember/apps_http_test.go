package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppsListReturnsBaselineEnabled(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	_, body := doReq(t, srv, http.MethodGet, "/v1/apps", "", "")
	apps, _ := body["apps"].([]any)
	if len(apps) < 2 {
		t.Fatalf("apps = %+v, want at least claude+codex baseline", body)
	}
	seen := map[string]bool{}
	for _, a := range apps {
		m := a.(map[string]any)
		seen[m["name"].(string)] = m["enabled"].(bool)
	}
	if v, ok := seen["claude"]; !ok || !v {
		t.Fatalf("claude not enabled by default: %+v", seen)
	}
	if v, ok := seen["codex"]; !ok || !v {
		t.Fatalf("codex not enabled by default: %+v", seen)
	}
}

func TestAppsPutHidesAndPersists(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if resp, _ := doReq(t, srv, http.MethodPut, "/v1/apps", "", `{"app":"codex","enabled":false}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("put status = %d", resp.StatusCode)
	}
	if hidden := app.hiddenAppsSet(); !hidden["codex"] {
		t.Fatalf("codex not in hidden set after PUT: %+v", hidden)
	}
	app.appsMu.Lock()
	app.hiddenApps = map[string]bool{} // clear in-memory set under the mutex, like production paths
	app.appsMu.Unlock()
	app.loadHiddenApps()
	if !app.hiddenAppsSet()["codex"] {
		t.Fatal("hidden set did not persist across loadHiddenApps()")
	}
	_, body := doReq(t, srv, http.MethodGet, "/v1/apps", "", "")
	for _, a := range body["apps"].([]any) {
		m := a.(map[string]any)
		if m["name"] == "codex" && m["enabled"] != false {
			t.Fatalf("codex still enabled after hide: %+v", m)
		}
	}
}
