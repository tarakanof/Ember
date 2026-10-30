package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"os"
	"path/filepath"
	"testing"
)

func TestClockTransportFollowsLiveKeepAlive(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://192.168.0.66"
	k := newClockAccess(func() *Config { return &cfg })

	if k.transport() != nil {
		t.Fatalf("default must use the default transport")
	}
	if _, err := k.client(callPublish); err != nil {
		t.Fatal(err)
	}

	off := false
	cfg.AWTRIX.KeepAlive = &off
	if _, ok := k.transport().(closeAfterReply); !ok {
		t.Fatalf("keep_alive=false did not select closeAfterReply")
	}

	on := true
	cfg.AWTRIX.KeepAlive = &on
	if k.transport() != nil {
		t.Fatalf("keep_alive=true did not restore the default transport")
	}
}

func TestKeepAliveEnabledDefaultsTrue(t *testing.T) {
	if !(AWTRIXConfig{}).KeepAliveEnabled() {
		t.Fatalf("nil keep_alive must mean enabled")
	}
	off := false
	if (AWTRIXConfig{KeepAlive: &off}).KeepAliveEnabled() {
		t.Fatalf("explicit false must disable")
	}
}

func TestParseConfigFile_KeepAliveFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"awtrix":{"keep_alive":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseConfigFile(path)
	if err != nil {
		t.Fatalf("parseConfigFile: %v", err)
	}
	if cfg.AWTRIX.KeepAlive == nil || cfg.AWTRIX.KeepAliveEnabled() {
		t.Fatalf("keep_alive = %v, want explicit false", cfg.AWTRIX.KeepAlive)
	}
	cfg.applyDefaults()
	if cfg.AWTRIX.KeepAliveEnabled() {
		t.Fatalf("applyDefaults overwrote an explicit false")
	}
}

func TestCloseAfterReplyEvictsIdlePooledConnection(t *testing.T) {
	var mu sync.Mutex
	var addrs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			return
		}
		mu.Lock()
		addrs = append(addrs, r.RemoteAddr)
		mu.Unlock()
	}))
	defer srv.Close()
	defer http.DefaultTransport.(interface{ CloseIdleConnections() }).CloseIdleConnections()

	resp, err := (&http.Client{}).Post(srv.URL+"/pool", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	cl := awtrix.NewClientWithTransport(srv.URL, 2*time.Second, closeAfterReply{})
	if err := cl.PushApp(context.Background(), "ember", map[string]any{"text": "x"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(addrs) != 2 || addrs[0] == addrs[1] {
		t.Fatalf("remote addrs = %v, want the closed push on a fresh connection", addrs)
	}
}
