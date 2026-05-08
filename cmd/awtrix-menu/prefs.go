package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type prefsHandler struct {
	envPath string

	mu           sync.Mutex           // protects nonces and expectedHost
	nonces       map[string]time.Time // nonce → expiry
	expectedHost string               // set once after listener binds; if empty, prefix-check fallback
}

func newPrefsHandler(envPath string) *prefsHandler {
	return &prefsHandler{envPath: envPath, nonces: map[string]time.Time{}}
}

// bindHost pins the handler to the exact host:port that the listener bound.
// Called once after the listener has bound a port.
func (h *prefsHandler) bindHost(host string) {
	h.mu.Lock()
	h.expectedHost = host
	h.mu.Unlock()
}

func (h *prefsHandler) issueNonce() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	n := base64.RawURLEncoding.EncodeToString(b[:])
	h.mu.Lock()
	h.nonces[n] = time.Now().Add(5 * time.Minute)
	h.mu.Unlock()
	return n
}

func (h *prefsHandler) checkNonce(n string, consume bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	exp, ok := h.nonces[n]
	if !ok || time.Now().After(exp) {
		delete(h.nonces, n)
		return false
	}
	if consume {
		delete(h.nonces, n)
	}
	return true
}

func (h *prefsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'none'; style-src 'unsafe-inline'")
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodPost:
		h.handlePost(w, r)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

const formTmpl = `<!doctype html>
<html><head><meta charset="utf-8"><title>awtrix-menu prefs</title>
<style>body{font-family:-apple-system,sans-serif;max-width:480px;margin:40px auto;padding:0 16px}label{display:block;margin-top:14px;font-weight:600}input[type=text],input[type=password]{width:100%;padding:8px;font-size:14px;box-sizing:border-box;border:1px solid #ccc;border-radius:4px}.err{background:#fee;border:1px solid #c44;padding:8px;border-radius:4px;color:#900}.ok{background:#efe;border:1px solid #4a4;padding:8px;border-radius:4px;color:#040}button{margin-top:16px;padding:8px 16px;font-size:14px}.hint{color:#666;font-size:12px;margin-top:4px}</style>
</head><body>
<h1>awtrix-menu prefs</h1>
{{if .Err}}<p class="err">{{.Err}}</p>{{end}}
{{if .Saved}}<p class="ok">Saved. You can close this tab.</p>{{end}}
<form method="post" action="/">
<input type="hidden" name="nonce" value="{{.Nonce}}">
<input type="hidden" name="env_mtime" value="{{.EnvMtime}}">
<label>STATUS_SOURCE
<input type="text" name="STATUS_SOURCE" value="{{.Source}}" required></label>
<label>STATUS_SERVER_URL
<input type="text" name="STATUS_SERVER_URL" value="{{.ServerURL}}" required></label>
<label>STATUS_TOKEN
<input type="password" name="STATUS_TOKEN" value="" placeholder="{{if .TokenSet}}(unchanged){{end}}"></label>
{{if .TokenSet}}<label class="hint"><input type="checkbox" name="clear_token" value="1"> Clear token</label>{{end}}
<button type="submit">Save</button>
</form></body></html>`

var formT = template.Must(template.New("form").Parse(formTmpl))

type formData struct {
	Nonce     string
	EnvMtime  string
	Source    string
	ServerURL string
	TokenSet  bool
	Err       string
	Saved     bool
}

func (h *prefsHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	nonce := r.URL.Query().Get("nonce")
	if !h.checkNonce(nonce, false) {
		http.Error(w, "session expired or invalid; click Preferences… again", 403)
		return
	}
	rec, _ := readEnv(h.envPath)
	if rec == nil {
		rec = &envRec{}
	}
	mt := int64(0)
	if info, err := os.Stat(h.envPath); err == nil {
		mt = info.ModTime().UnixNano()
	}
	d := formData{
		Nonce:     nonce,
		EnvMtime:  strconv.FormatInt(mt, 10),
		Source:    rec.get("STATUS_SOURCE"),
		ServerURL: rec.get("STATUS_SERVER_URL"),
		TokenSet:  rec.get("STATUS_TOKEN") != "",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = formT.Execute(w, d)
}

var ctrlChars = func() string {
	var s []byte
	for i := 0; i < 32; i++ {
		s = append(s, byte(i))
	}
	s = append(s, 127)
	return string(s)
}()

func (h *prefsHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form: "+err.Error(), 400)
		return
	}
	nonce := r.PostFormValue("nonce")
	// Host check (defeat DNS rebinding)
	host := r.Host
	h.mu.Lock()
	pinned := h.expectedHost
	h.mu.Unlock()
	if pinned != "" {
		// Production path: require exact match against the bound port.
		if host != pinned {
			http.Error(w, "bad Host", 403)
			return
		}
	} else {
		// Test path (handler used standalone, no bound listener): prefix check.
		if !strings.HasPrefix(host, "127.0.0.1:") {
			http.Error(w, "bad Host", 403)
			return
		}
	}
	// Origin check
	expectedOrigin := "http://" + host
	origin := r.Header.Get("Origin")
	if origin == "" {
		http.Error(w, "missing Origin", 403)
		return
	}
	if origin != expectedOrigin {
		http.Error(w, "bad Origin", 403)
		return
	}
	// Nonce check (don't consume yet — only after success)
	if !h.checkNonce(nonce, false) {
		http.Error(w, "invalid or expired nonce", 403)
		return
	}
	// Validate env_mtime
	wantMtime, err := strconv.ParseInt(r.PostFormValue("env_mtime"), 10, 64)
	if err != nil {
		http.Error(w, "bad env_mtime", 400)
		return
	}
	curMtime := int64(0)
	if info, err := os.Stat(h.envPath); err == nil {
		curMtime = info.ModTime().UnixNano()
	}
	if wantMtime != curMtime {
		// Stale: consume the nonce so the user must reload
		h.checkNonce(nonce, true)
		http.Error(w, "env file changed since you opened the form; click Preferences… again", 409)
		return
	}
	// Validate fields
	src := r.PostFormValue("STATUS_SOURCE")
	srvURL := r.PostFormValue("STATUS_SERVER_URL")
	tok := r.PostFormValue("STATUS_TOKEN")
	clear := r.PostFormValue("clear_token") == "1"
	if src == "" || srvURL == "" {
		http.Error(w, "STATUS_SOURCE and STATUS_SERVER_URL are required", 400)
		return
	}
	for _, v := range []string{src, srvURL, tok} {
		if strings.ContainsAny(v, ctrlChars) {
			http.Error(w, "values may not contain control characters", 400)
			return
		}
	}
	if u, err := url.Parse(srvURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		http.Error(w, "STATUS_SERVER_URL must be http(s) with no embedded credentials", 400)
		return
	}
	// Token preservation rule
	rec, err := readEnv(h.envPath)
	if err != nil {
		rec = &envRec{}
	}
	rec.set("STATUS_SOURCE", src)
	rec.set("STATUS_SERVER_URL", srvURL)
	switch {
	case clear:
		rec.set("STATUS_TOKEN", "")
	case tok != "":
		rec.set("STATUS_TOKEN", tok)
	default:
		// blank field + no Clear checkbox: leave STATUS_TOKEN untouched
	}
	// Refuse if the config dir is wider than 0700 (spec: prevents same-laptop
	// non-owner reads of producer.env's directory).
	if info, err := os.Stat(filepath.Dir(h.envPath)); err == nil {
		if info.IsDir() && info.Mode().Perm()&0o077 != 0 {
			http.Error(w, "config dir is wider than 0700; run `chmod 0700 "+filepath.Dir(h.envPath)+"` and try again", 500)
			return
		}
	}
	// Atomic write
	if err := writeEnvAtomic(h.envPath, rec.serialize()); err != nil {
		http.Error(w, "save failed: "+err.Error(), 500)
		return
	}
	// Consume nonce
	h.checkNonce(nonce, true)
	// Re-render with success banner
	mt := int64(0)
	if info, err := os.Stat(h.envPath); err == nil {
		mt = info.ModTime().UnixNano()
	}
	_ = formT.Execute(w, formData{
		Nonce:     "", // reissue on next click
		EnvMtime:  strconv.FormatInt(mt, 10),
		Source:    src,
		ServerURL: srvURL,
		TokenSet:  rec.get("STATUS_TOKEN") != "",
		Saved:     true,
	})
}

// writeEnvAtomic writes content to path with mode 0600 via temp file + rename.
// Hard-fails if chmod fails (token-bearing file).
func writeEnvAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*.env")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("chmod 0600 failed: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// prefsServer wraps prefsHandler with a lazily-started loopback HTTP listener.
// The server is shared across Preferences… clicks; each click issues a fresh nonce.
type prefsServer struct {
	once    sync.Once
	handler *prefsHandler
	srv     *http.Server
	url     string
	err     error
}

func newPrefsServer(envPath string) *prefsServer {
	return &prefsServer{handler: newPrefsHandler(envPath)}
}

func (p *prefsServer) start() error {
	p.once.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			p.err = err
			return
		}
		addr := ln.Addr().(*net.TCPAddr)
		p.handler.bindHost(fmt.Sprintf("127.0.0.1:%d", addr.Port))
		p.srv = &http.Server{
			Handler:           p.handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		p.url = fmt.Sprintf("http://127.0.0.1:%d", addr.Port)
		go func() { _ = p.srv.Serve(ln) }()
	})
	return p.err
}

func (p *prefsServer) urlForClick() string {
	if err := p.start(); err != nil {
		return ""
	}
	return p.url + "/?nonce=" + p.handler.issueNonce()
}

func (p *prefsServer) shutdown(ctx context.Context) error {
	if p.srv != nil {
		return p.srv.Shutdown(ctx)
	}
	return nil
}
