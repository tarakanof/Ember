package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestPlist_StructureCorrect(t *testing.T) {
	data := generatePlist("/abs/path/to/awtrix-menu", "/Users/joe", 501)
	s := string(data)
	for _, want := range []string{
		"<key>Label</key>",
		"<string>com.awtrix-ai-status.menu</string>",
		"<string>/abs/path/to/awtrix-menu</string>",
		"<string>run</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<dict>", // KeepAlive is a dict
		"<key>SuccessfulExit</key>",
		"<false/>", // i.e., only restart on non-zero exit
		"<key>LimitLoadToSessionType</key>",
		"<string>Aqua</string>",
		"/Users/joe/Library/Logs/awtrix-menu.log",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	if strings.Contains(s, "<key>KeepAlive</key>\n    <true/>") {
		t.Error("must NOT use KeepAlive=true; conditional dict only")
	}
	idx := strings.Index(s, "<plist")
	if err := xml.Unmarshal([]byte(s[idx:]), new(any)); err != nil {
		t.Errorf("plist body not well-formed XML: %v", err)
	}
}

func TestPlist_EscapesPaths(t *testing.T) {
	data := generatePlist("/Users/<user>&path/awtrix-menu", "/Users/joe", 501)
	if strings.Contains(string(data), "<user>&path") {
		t.Errorf("path not XML-escaped")
	}
}
