package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPrefs_GetWithoutNonce_403(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(newPrefsHandler(filepath.Join(dir, "producer.env")))
	defer srv.Close()
	resp, _ := srv.Client().Get(srv.URL + "/")
	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestPrefs_GetWithValidNonce_RendersForm(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "producer.env")
	_ = os.WriteFile(envPath, []byte("STATUS_SOURCE=test\nSTATUS_SERVER_URL=http://x\nSTATUS_TOKEN=secret\n"), 0o600)
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	resp, _ := srv.Client().Get(srv.URL + "/?nonce=" + nonce)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	for _, want := range []string{`name="STATUS_SOURCE"`, `value="test"`, `value="http://x"`, `placeholder="(unchanged)"`, `name="env_mtime"`, `name="nonce"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("form missing %q", want)
		}
	}
	// Token must NOT appear in the rendered HTML
	if strings.Contains(string(body), "secret") {
		t.Errorf("rendered HTML contains the token!")
	}
	// CSP header
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("missing CSP script-src 'none', got: %s", csp)
	}
}

func TestPrefs_PostBadOrigin_403(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "producer.env")
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(url.Values{
		"nonce":             {nonce},
		"env_mtime":         {"0"},
		"STATUS_SOURCE":     {"x"},
		"STATUS_SERVER_URL": {"http://y"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403 (bad Origin)", resp.StatusCode)
	}
}

func TestPrefs_PostMissingOrigin_403(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "producer.env")
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(url.Values{
		"nonce":             {nonce},
		"env_mtime":         {"0"},
		"STATUS_SOURCE":     {"x"},
		"STATUS_SERVER_URL": {"http://y"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// no Origin set
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403 (missing Origin)", resp.StatusCode)
	}
}

func TestPrefs_PostValid_WritesEnvFileAtomicallyAndPreservesToken(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "producer.env")
	_ = os.WriteFile(envPath, []byte("STATUS_TOKEN=keepme\n"), 0o600)
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	postForm := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {fmtInt64(mtime)},
		"STATUS_SOURCE":     {"new-source"},
		"STATUS_SERVER_URL": {"http://new"},
		"STATUS_TOKEN":      {""}, // blank → preserve existing
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	body, _ := os.ReadFile(envPath)
	bodyStr := string(body)
	for _, want := range []string{"STATUS_SOURCE=new-source", "STATUS_SERVER_URL=http://new", "STATUS_TOKEN=keepme"} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("file missing %q\ngot: %s", want, bodyStr)
		}
	}
	info, _ = os.Stat(envPath)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perms = %#o, want 0600", info.Mode().Perm())
	}
}

func TestPrefs_PostStaleMtime_409(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "producer.env")
	_ = os.WriteFile(envPath, []byte("STATUS_SOURCE=x\n"), 0o600)
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	postForm := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {"1"}, // bogus old mtime
		"STATUS_SOURCE":     {"y"},
		"STATUS_SERVER_URL": {"http://z"},
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 409 {
		t.Errorf("status = %d, want 409 (stale mtime)", resp.StatusCode)
	}
}

func TestPrefs_PostReusedNonce_403(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "producer.env")
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	doPost := func() *http.Response {
		postForm := url.Values{
			"nonce":             {nonce},
			"env_mtime":         {"0"},
			"STATUS_SOURCE":     {"x"},
			"STATUS_SERVER_URL": {"http://y"},
		}
		req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", srv.URL)
		resp, _ := srv.Client().Do(req)
		return resp
	}
	resp1 := doPost()
	if resp1.StatusCode != 200 {
		t.Fatalf("first POST status = %d", resp1.StatusCode)
	}
	resp2 := doPost()
	if resp2.StatusCode != 403 {
		t.Errorf("reused nonce status = %d, want 403", resp2.StatusCode)
	}
}

func TestPrefs_RejectsNewlineInValue(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "producer.env")
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	postForm := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {"0"},
		"STATUS_SOURCE":     {"injected\nEVIL=yes"},
		"STATUS_SERVER_URL": {"http://x"},
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 400 {
		t.Errorf("newline injection status = %d, want 400", resp.StatusCode)
	}
}

// helper used in tests
func fmtInt64(n int64) string {
	return strconv.FormatInt(n, 10)
}
