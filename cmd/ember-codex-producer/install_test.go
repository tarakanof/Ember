package main

import (
	"strings"
	"testing"
)

func TestGeneratePlist_DaemonShape(t *testing.T) {
	out := string(generatePlist("/Users/x/go/bin/ember-codex-producer", "/Users/x"))
	for _, want := range []string{
		"com.ember.codex",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
		"<string>run</string>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "StartInterval") {
		t.Error("daemon plist must not use StartInterval")
	}
	if strings.Contains(out, "StandardOutPath") || strings.Contains(out, "StandardErrorPath") {
		t.Error("plist must not set StandardOutPath/StandardErrorPath; the daemon opens its own log via producer.OpenDaemonLog")
	}
}

func TestGeneratePlist_NoToken(t *testing.T) {
	out := string(generatePlist("/Users/x/go/bin/ember-codex-producer", "/Users/x"))
	if strings.Contains(out, "Bearer") || strings.Contains(out, "EMBER_TOKEN") {
		t.Errorf("plist must not contain token material:\n%s", out)
	}
}
