package main

import (
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
