package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
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

func TestMergeSettings_FreshInstall(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/awtrix-claude-producer"); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(tmp, ".claude", "settings.json")
	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"PreToolUse"`, `"UserPromptSubmit"`, `"Stop"`, `"PermissionRequest"`,
		`/usr/local/bin/awtrix-claude-producer hook`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("settings.json missing %q\nbody: %s", want, body)
		}
	}
}

func TestMergeSettings_PreservesUserPermissions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"permissions":{"allow":["Bash(grep:*)"]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/awtrix-claude-producer"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if !strings.Contains(string(body), `Bash(grep:*)`) {
		t.Errorf("user permissions block dropped: %s", body)
	}
	if !strings.Contains(string(body), `awtrix-claude-producer hook`) {
		t.Errorf("hooks not added: %s", body)
	}
}

func TestMergeSettings_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/awtrix-claude-producer"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/awtrix-claude-producer"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if string(first) != string(second) {
		t.Errorf("re-running install changed settings:\nfirst: %s\nsecond: %s", first, second)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestMergeSettingsJSON_StatusLineCaptureAndSet(t *testing.T) {
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustMkdir(t, filepath.Join(home, ".config", "awtrix-ai-status"))
	settings := filepath.Join(home, ".claude", "settings.json")
	sidecar := filepath.Join(home, ".config", "awtrix-ai-status", "wrapped-statusline.json")

	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"mine.sh"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(home, "/x/awtrix-claude-producer"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(sidecar)
	if err != nil || !strings.Contains(string(raw), "mine.sh") {
		t.Fatalf("sidecar missing user command: err=%v raw=%s", err, raw)
	}
	sb, _ := os.ReadFile(settings)
	if !strings.Contains(string(sb), "awtrix-claude-producer statusline") {
		t.Errorf("statusLine not set to ours: %s", sb)
	}
	// Idempotent re-install must NOT capture our own command as wrapped.
	if err := mergeSettingsJSON(home, "/x/awtrix-claude-producer"); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(sidecar)
	if !strings.Contains(string(raw2), "mine.sh") || strings.Contains(string(raw2), "awtrix-claude-producer statusline") {
		t.Errorf("re-install corrupted sidecar: %s", raw2)
	}
}
