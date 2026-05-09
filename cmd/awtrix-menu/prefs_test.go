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
	"sync"
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
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
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
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
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

func TestPrefs_PostHostNotLoopbackIP_403(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "producer.env")
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	// Build a request with Host header forced to localhost:<port>
	u, _ := url.Parse(srv.URL)
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(url.Values{
		"nonce":             {nonce},
		"env_mtime":         {"0"},
		"STATUS_SOURCE":     {"x"},
		"STATUS_SERVER_URL": {"http://y"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "localhost:" + u.Port()
	req.Header.Set("Origin", "http://localhost:"+u.Port())
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403 (non-127.0.0.1 Host)", resp.StatusCode)
	}
}

func TestPrefs_PostRefusesWideConfigDir(t *testing.T) {
	dir := t.TempDir()
	// Make the config dir world-readable (simulate user mistake).
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	if err := os.WriteFile(envPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	postForm := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {strconv.FormatInt(mtime, 10)},
		"STATUS_SOURCE":     {"x"},
		"STATUS_SERVER_URL": {"http://y"},
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 500 {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 500 (wide config dir); body=%s", resp.StatusCode, body)
	}
}

func TestPrefs_PostValidationError_RendersFormWithBanner(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	if err := os.WriteFile(envPath, []byte("STATUS_SOURCE=x\nSTATUS_SERVER_URL=http://y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	// Submit empty STATUS_SOURCE to trigger validation failure.
	postForm := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {strconv.FormatInt(mtime, 10)},
		"STATUS_SOURCE":     {""},
		"STATUS_SERVER_URL": {"http://y"},
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `class="err"`) {
		t.Errorf("response missing error banner; body=%s", s)
	}
	if !strings.Contains(s, `name="nonce"`) {
		t.Errorf("response missing form (no nonce input); body=%s", s)
	}
	if !strings.Contains(s, nonce) {
		t.Errorf("response did not preserve nonce; body=%s", s)
	}
}

func TestPrefs_PostWrongPort_403(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	h := newPrefsHandler(envPath)
	// Pin the handler to a port that won't match what httptest hands us.
	h.bindHost("127.0.0.1:1")
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
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403 (wrong port)", resp.StatusCode)
	}
}

func TestPrefs_PostValidationError_PreservesSubmittedValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	// Disk has the old values.
	if err := os.WriteFile(envPath, []byte("STATUS_SOURCE=old-source\nSTATUS_SERVER_URL=http://old-host\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	// Submit a *new* source plus a malformed URL — validation fails on the URL,
	// but the new source should be echoed back to the form so the user doesn't
	// lose their edit.
	postForm := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {strconv.FormatInt(mtime, 10)},
		"STATUS_SOURCE":     {"new-source-typed"},
		"STATUS_SERVER_URL": {"ftp://nope"},
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `value="new-source-typed"`) {
		t.Errorf("rendered form lost the submitted source; body=%s", s)
	}
	if !strings.Contains(s, `value="ftp://nope"`) {
		t.Errorf("rendered form lost the submitted server URL; body=%s", s)
	}
	if strings.Contains(s, `value="old-source"`) {
		t.Errorf("rendered form should echo submitted values, not stale disk values; body=%s", s)
	}
}

func TestPrefs_PostValidationError_PreservesIntentionallyClearedField(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	// Disk has both fields populated.
	if err := os.WriteFile(envPath, []byte("STATUS_SOURCE=on-disk\nSTATUS_SERVER_URL=http://on-disk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	// User intentionally cleared STATUS_SOURCE (required) and submits — the
	// validation error must re-render with the field empty, NOT silently
	// restoring the on-disk value.
	postForm := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {strconv.FormatInt(mtime, 10)},
		"STATUS_SOURCE":     {""},
		"STATUS_SERVER_URL": {"http://kept"},
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if strings.Contains(s, `value="on-disk"`) {
		t.Errorf("rendered form silently restored cleared source from disk; body=%s", s)
	}
	if !strings.Contains(s, `value="http://kept"`) {
		t.Errorf("rendered form lost the kept server URL; body=%s", s)
	}
}

func TestPrefs_ConcurrentPosts_SameNonce_OnlyOneSucceeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	if err := os.WriteFile(envPath, []byte("STATUS_TOKEN=keepme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	do := func() int {
		postForm := url.Values{
			"nonce":             {nonce},
			"env_mtime":         {strconv.FormatInt(mtime, 10)},
			"STATUS_SOURCE":     {"x"},
			"STATUS_SERVER_URL": {"http://y"},
		}
		req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", srv.URL)
		resp, _ := srv.Client().Do(req)
		return resp.StatusCode
	}
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := range statuses {
		i := i
		wg.Add(1)
		go func() { defer wg.Done(); statuses[i] = do() }()
	}
	wg.Wait()
	successes := 0
	rejections := 0
	for _, s := range statuses {
		switch s {
		case 200:
			successes++
		case 403:
			rejections++
		}
	}
	if successes != 1 || rejections != 1 {
		t.Errorf("statuses = %v, want exactly one 200 and one 403", statuses)
	}
}

func TestPrefs_PostRefusesUnreadableEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	// Write a file, then chmod 000 so readEnv fails with permission denied.
	if err := os.WriteFile(envPath, []byte("STATUS_SOURCE=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(envPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(envPath, 0o600) })
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	postForm := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {strconv.FormatInt(mtime, 10)},
		"STATUS_SOURCE":     {"y"},
		"STATUS_SERVER_URL": {"http://z"},
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500 (unreadable env)", resp.StatusCode)
	}
}

func TestPrefsHandler_ClaimReleaseNonce(t *testing.T) {
	h := newPrefsHandler("/dev/null")
	n := h.issueNonce()

	// First claim succeeds.
	if !h.claimNonce(n) {
		t.Fatal("first claimNonce should succeed")
	}
	// Concurrent claim returns false (in-flight).
	if h.claimNonce(n) {
		t.Error("second claimNonce while in-flight should return false")
	}
	// Release without success keeps the nonce live for retry.
	h.releaseNonce(n, false)
	if !h.claimNonce(n) {
		t.Error("after release(false), claimNonce should succeed again (retry path)")
	}
	// Release with success consumes the nonce.
	h.releaseNonce(n, true)
	if h.claimNonce(n) {
		t.Error("after release(true), claimNonce should fail (consumed)")
	}
}

func TestPrefs_NoncePreservedAfter400_ThenSucceedsOnRetry(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	if err := os.WriteFile(envPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()

	// First POST: bad URL scheme → 400, nonce should remain alive.
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	bad := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {strconv.FormatInt(mtime, 10)},
		"STATUS_SOURCE":     {"x"},
		"STATUS_SERVER_URL": {"ftp://nope"},
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(bad.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != 400 {
		t.Fatalf("first POST status = %d, want 400", resp.StatusCode)
	}

	// Second POST with corrected URL using the same nonce should succeed.
	info, _ = os.Stat(envPath)
	mtime = info.ModTime().UnixNano()
	good := url.Values{
		"nonce":             {nonce},
		"env_mtime":         {strconv.FormatInt(mtime, 10)},
		"STATUS_SOURCE":     {"x"},
		"STATUS_SERVER_URL": {"http://y"},
	}
	req2, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(good.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Origin", srv.URL)
	resp2, _ := srv.Client().Do(req2)
	if resp2.StatusCode != 200 {
		body, _ := io.ReadAll(resp2.Body)
		t.Errorf("second POST status = %d, want 200; body=%s", resp2.StatusCode, body)
	}
}

func TestPrefs_PostRejectsHostlessURL(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "producer.env")
	if err := os.WriteFile(envPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPrefsHandler(envPath)
	srv := httptest.NewServer(h)
	defer srv.Close()
	nonce := h.issueNonce()
	info, _ := os.Stat(envPath)
	mtime := info.ModTime().UnixNano()
	cases := []struct {
		name string
		url  string
	}{
		{"opaque", "http:foo"},
		{"scheme_only", "http:"},
		{"empty_authority", "http://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			postForm := url.Values{
				"nonce":             {nonce},
				"env_mtime":         {strconv.FormatInt(mtime, 10)},
				"STATUS_SOURCE":     {"x"},
				"STATUS_SERVER_URL": {tc.url},
			}
			req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(postForm.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", srv.URL)
			resp, _ := srv.Client().Do(req)
			if resp.StatusCode != 400 {
				t.Errorf("status = %d, want 400 (hostless URL rejected)", resp.StatusCode)
			}
		})
	}
}

// helper used in tests
func fmtInt64(n int64) string {
	return strconv.FormatInt(n, 10)
}
