package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestGeneratePlist_StructureAndPaths(t *testing.T) {
	data, err := generatePlist("/abs/path/to/awtrix-claude-producer", "/Users/joe", 501)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "<key>Label</key>") {
		t.Errorf("missing Label key")
	}
	if !strings.Contains(s, "<string>com.awtrix-ai-status.heartbeat</string>") {
		t.Errorf("wrong Label value")
	}
	if !strings.Contains(s, "<string>/abs/path/to/awtrix-claude-producer</string>") {
		t.Errorf("missing absolute binary path")
	}
	if !strings.Contains(s, "<integer>10</integer>") {
		t.Errorf("missing StartInterval=10")
	}
	if !strings.Contains(s, "/Users/joe/Library/Logs/awtrix-ai-status-tick.log") {
		t.Errorf("missing log path")
	}
	// XML well-formed (skip the DOCTYPE which encoding/xml doesn't parse)
	idx := strings.Index(s, "<plist")
	if idx < 0 {
		t.Fatal("missing <plist>")
	}
	var v any
	if err := xml.Unmarshal([]byte(s[idx:]), &v); err != nil {
		t.Errorf("plist body not well-formed XML: %v", err)
	}
}

func TestGeneratePlist_EscapesPathWithSpecialChars(t *testing.T) {
	data, err := generatePlist("/Users/<weird>&path/awtrix-claude-producer", "/Users/joe", 501)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "<weird>&path") {
		t.Errorf("special chars not escaped: %s", string(data))
	}
	if !strings.Contains(string(data), "&lt;weird&gt;&amp;path") {
		t.Errorf("expected XML-escaped path")
	}
}

func TestShellSafePath(t *testing.T) {
	cases := []struct {
		path string
		ok   bool
	}{
		{"/usr/local/bin/awtrix-claude-producer", true},
		{"/Users/joe/go/bin/awtrix-claude-producer", true},
		{"/path with space/bin", false},
		{"/path/with$dollar", false},
		{"/path/with`backtick", false},
	}
	for _, c := range cases {
		if got := shellSafePath(c.path); got != c.ok {
			t.Errorf("shellSafePath(%q) = %v, want %v", c.path, got, c.ok)
		}
	}
}

func TestProducerEnvExample_Content(t *testing.T) {
	got := producerEnvExampleContent()
	for _, want := range []string{"STATUS_SOURCE=", "STATUS_SERVER_URL=", "STATUS_TOKEN="} {
		if !strings.Contains(got, want) {
			t.Errorf("env example missing %q", want)
		}
	}
}
