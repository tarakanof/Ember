package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarakanof/ember/internal/producer"
)

func TestGeneratePlist_StructureAndPaths(t *testing.T) {
	data, err := generatePlist("/abs/path/to/ember-claude-producer", "/Users/joe", 501)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "<key>Label</key>") {
		t.Errorf("missing Label key")
	}
	if !strings.Contains(s, "<string>com.ember.heartbeat</string>") {
		t.Errorf("wrong Label value")
	}
	if !strings.Contains(s, "<string>/abs/path/to/ember-claude-producer</string>") {
		t.Errorf("missing absolute binary path")
	}
	if !strings.Contains(s, "<string>run</string>") {
		t.Errorf("plist should launch the long-lived `run` daemon")
	}
	if strings.Contains(s, "<key>StartInterval</key>") {
		t.Errorf("daemon plist must not use StartInterval (it loops internally)")
	}
	if !strings.Contains(s, "<key>KeepAlive</key>") {
		t.Errorf("daemon plist must set KeepAlive so launchd restarts it after a crash/eviction")
	}
	if strings.Contains(s, "StandardOutPath") || strings.Contains(s, "StandardErrorPath") {
		t.Errorf("plist must not set StandardOutPath/StandardErrorPath; the daemon opens its own log via producer.OpenDaemonLog:\n%s", s)
	}
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
	data, err := generatePlist("/Users/<weird>&path/ember-claude-producer", "/Users/joe", 501)
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
		{"/usr/local/bin/ember-claude-producer", true},
		{"/Users/joe/go/bin/ember-claude-producer", true},
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
	for _, want := range []string{"EMBER_SOURCE=", "EMBER_SERVER_URL=", "EMBER_TOKEN="} {
		if !strings.Contains(got, want) {
			t.Errorf("env example missing %q", want)
		}
	}
}

func TestMergeSettings_FreshInstall(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(tmp, ".claude", "settings.json")
	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"PreToolUse"`, `"UserPromptSubmit"`, `"Stop"`, `"PermissionRequest"`,
		`\"/usr/local/bin/ember-claude-producer\" hook`,
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
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if !strings.Contains(string(body), `Bash(grep:*)`) {
		t.Errorf("user permissions block dropped: %s", body)
	}
	if !strings.Contains(string(body), `ember-claude-producer\" hook`) {
		t.Errorf("hooks not added: %s", body)
	}
}

func TestMergeSettings_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if string(first) != string(second) {
		t.Errorf("re-running install changed settings:\nfirst: %s\nsecond: %s", first, second)
	}
}

func TestProducerHookEntries_SelfHealingGuard(t *testing.T) {
	bin := "/Applications/Ember.app/Contents/MacOS/ember-claude-producer"
	for _, e := range producerHookEntries(bin) {
		if !strings.HasPrefix(e.command, `[ -x "`+bin+`" ] && `) {
			t.Errorf("hook %q command not guarded: %s", e.event, e.command)
		}
		if !strings.HasSuffix(strings.TrimSpace(e.command), "|| true") {
			t.Errorf("hook %q command missing `|| true`: %s", e.event, e.command)
		}
	}
}

func TestEntryMatchesProducer_GuardedCommand(t *testing.T) {
	bin := "/Applications/Ember.app/Contents/MacOS/ember-claude-producer"
	entry := producerHookEntries(bin)[0]
	asAny := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": entry.command}}}
	if !entryMatchesProducer(asAny) {
		t.Errorf("entryMatchesProducer did not recognize guarded command: %s", entry.command)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestMergeSettings_SessionEndMatcherIncludesClear(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"logout|prompt_input_exit|other|clear|resume"`) {
		t.Errorf("SessionEnd matcher should be the current reason set (clear, resume; not the removed bypass_permissions_disabled): %s", body)
	}
}

func TestMergeSettings_UpgradeReplacesOldSessionEndMatcher(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldMatcherSettings := `{"hooks":{"SessionEnd":[{"matcher":"logout|prompt_input_exit|bypass_permissions_disabled|other","hooks":[{"type":"command","command":"/usr/local/bin/ember-claude-producer hook session-end"}]}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(oldMatcherSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"logout|prompt_input_exit|bypass_permissions_disabled|other"`) {
		t.Errorf("upgrade left the old matcher without clear (would ship without the /clear fix):\n%s", body)
	}
	if !strings.Contains(string(body), `"logout|prompt_input_exit|other|clear|resume"`) {
		t.Errorf("upgrade did not install the current matcher:\n%s", body)
	}
	if strings.Count(string(body), `"SessionEnd"`) != 1 {
		t.Errorf("expected exactly one SessionEnd key, got: %s", body)
	}
}

func TestMergeSettings_NotificationMatcherIncludesNewSignals(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"matcher": "`+notificationMatcher+`"`) {
		t.Errorf("Notification matcher missing new signals: %s", body)
	}
}

func TestMergeSettings_UpgradeReplacesOldNotificationMatcher(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldMatcherSettings := `{"hooks":{"Notification":[{"matcher":"permission_prompt","hooks":[{"type":"command","command":"/usr/local/bin/ember-claude-producer hook notification"}]}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(oldMatcherSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"matcher": "`+notificationMatcher+`"`) {
		t.Errorf("upgrade did not install the new Notification matcher:\n%s", body)
	}
	if strings.Count(string(body), `"Notification"`) != 1 {
		t.Errorf("expected exactly one Notification key, got: %s", body)
	}
}

func TestMergeSettings_RegistersOutcomeHooks(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"PostToolUse"`, `"PostToolUseFailure"`, `"PermissionDenied"`,
		`hook post-tool-use `, `hook post-tool-use-failure `, `hook permission-denied `,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("settings.json missing %q\nbody: %s", want, body)
		}
	}
}

func TestMergeSettingsJSON_StatusLineCaptureAndSet(t *testing.T) {
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustMkdir(t, filepath.Join(home, ".config", "ember"))
	settings := filepath.Join(home, ".claude", "settings.json")
	sidecar := filepath.Join(home, ".config", "ember", "wrapped-statusline.json")

	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"mine.sh"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(home, "/x/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(sidecar)
	if err != nil || !strings.Contains(string(raw), "mine.sh") {
		t.Fatalf("sidecar missing user command: err=%v raw=%s", err, raw)
	}
	sb, _ := os.ReadFile(settings)
	if !strings.Contains(string(sb), "ember-claude-producer statusline") {
		t.Errorf("statusLine not set to ours: %s", sb)
	}
	if err := mergeSettingsJSON(home, "/x/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(sidecar)
	if !strings.Contains(string(raw2), "mine.sh") || strings.Contains(string(raw2), "ember-claude-producer statusline") {
		t.Errorf("re-install corrupted sidecar: %s", raw2)
	}
}

func TestConfigureAt_RewritesPlaceholderSource(t *testing.T) {
	defer producer.SetHostNameForTest("Dmitrys-MacBook-Air")()
	home := t.TempDir()
	envDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(envDir, "producer.env")
	if err := os.WriteFile(envPath, []byte("EMBER_SOURCE=set-me-to-this-laptop-id\nEMBER_TOKEN=t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := configureAt(home, "/bin/true"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(envPath)
	if !strings.Contains(string(b), "EMBER_SOURCE=mba\n") || !strings.Contains(string(b), "EMBER_TOKEN=t") {
		t.Errorf("env = %q", b)
	}
	if st, _ := os.Stat(envPath); st.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", st.Mode().Perm())
	}
}

// Without a timeout Claude Code allows a settings hook 600 s, so a wedged
// producer would stall the session (#258).
func TestMergeSettings_BlockingHooksHaveTimeouts(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Async   bool    `json:"async"`
				Timeout float64 `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	for ev, groups := range root.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if !h.Async && (h.Timeout <= 0 || h.Timeout > 5) {
					t.Errorf("%s: timeout %v, want 0 < t <= 5", ev, h.Timeout)
				}
			}
		}
	}
	if got := root.Hooks["SessionEnd"][0].Hooks[0].Timeout; got > 2 {
		t.Errorf("SessionEnd timeout %v raises the 1.5 s exit budget too far", got)
	}
}
