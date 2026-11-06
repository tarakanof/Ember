package main

import (
	"strings"
	"testing"
)

func TestGeneratePlistDaemonShape(t *testing.T) {
	out := string(service.Plist("/Users/x/go/bin/ember-t3-producer"))
	for _, want := range []string{
		"<string>com.ember.t3</string>",
		"<string>/Users/x/go/bin/ember-t3-producer</string>",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
		"<string>run</string>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "StandardOutPath") || strings.Contains(out, "EMBER_TOKEN") {
		t.Error("plist must not set log paths or token material")
	}
}

func TestGeneratePlistEscapesPath(t *testing.T) {
	out := string(service.Plist("/Users/a&b/ember-t3-producer"))
	if !strings.Contains(out, "a&amp;b") {
		t.Fatalf("path not XML-escaped:\n%s", out)
	}
}
