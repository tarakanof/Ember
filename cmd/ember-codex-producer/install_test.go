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
		"/Users/x/Library/Logs/ember-codex-producer.log",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "StartInterval") {
		t.Error("daemon plist must not use StartInterval")
	}
}

func TestGeneratePlist_NoToken(t *testing.T) {
	out := string(generatePlist("/Users/x/go/bin/ember-codex-producer", "/Users/x"))
	if strings.Contains(out, "Bearer") || strings.Contains(out, "EMBER_TOKEN") {
		t.Errorf("plist must not contain token material:\n%s", out)
	}
}
