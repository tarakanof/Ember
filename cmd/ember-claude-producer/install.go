package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

const launchAgentLabel = "com.ember.heartbeat"

const (
	producerName       = "ember-claude-producer"
	legacyProducerName = "awtrix-claude-producer"
)

func runInstall() {
	if err := install(); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		os.Exit(1)
	}
	printPluginNote()
	fmt.Println("Install complete. Edit ~/.config/ember/producer.env, then restart `claude`.")
}

func runConfigure() {
	if err := configure(); err != nil {
		fmt.Fprintln(os.Stderr, "configure failed:", err)
		os.Exit(1)
	}
	printPluginNote()
	fmt.Println("Configure complete. Edit ~/.config/ember/producer.env, then restart `claude`.")
}

func install() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	uid := os.Getuid()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if err := producer.CheckInstallAllowed(producer.ExecLaunchctl, uid, launchAgentLabel, plistPath); err != nil {
		return err
	}
	if err := configure(); err != nil {
		return err
	}
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	plistData, err := generatePlist(binPath, home, uid)
	if err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, plistData, 0o644); err != nil {
		return err
	}
	return reloadLaunchAgent(producer.ExecLaunchctl, uid, plistPath)
}

func configureAt(home, binPath string) error {
	if err := createInstallDirs(home); err != nil {
		return err
	}
	envPath := filepath.Join(home, ".config", "ember", "producer.env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if err := os.WriteFile(envPath, []byte(producerEnvExampleContent()), 0o600); err != nil {
			return err
		}
	}
	if err := mergeSettingsJSON(home, binPath); err != nil {
		return err
	}
	removeSpikeLog(home)
	return nil
}

func spikeLogPath(home string) string {
	return filepath.Join(home, ".local", "state", "ember", "spike-hooks.jsonl")
}

func removeSpikeLog(home string) {
	_ = os.Remove(spikeLogPath(home))
}

func configure() error {
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	if !shellSafePath(binPath) {
		return fmt.Errorf("binary path contains shell metacharacters: %s\nMove the binary to a path without spaces or special chars", binPath)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return configureAt(home, binPath)
}

func createInstallDirs(home string) error {
	for _, d := range []string{
		filepath.Join(home, ".config", "ember"),
		filepath.Join(home, ".local", "state", "ember", "sessions"),
		filepath.Join(home, "Library", "Logs"),
		filepath.Join(home, "Library", "LaunchAgents"),
	} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func shellSafePath(p string) bool {
	for _, c := range p {
		switch c {
		case ' ', '\t', '"', '\'', '\\', '$', '`', ';', '|', '&', '>', '<',
			'*', '?', '(', ')', '{', '}', '!', '#', '\n':
			return false
		}
	}
	return true
}

func generatePlist(binPath, home string, uid int) ([]byte, error) {
	const tmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>run</string>
    </array>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>Nice</key>
    <integer>10</integer>
    <key>LowPriorityIO</key>
    <true/>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    </dict>
</dict>
</plist>
`
	out := fmt.Sprintf(tmpl,
		xmlEscape(launchAgentLabel),
		xmlEscape(binPath),
	)
	return []byte(out), nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func producerEnvExampleContent() string {
	return producer.EnvExample()
}

func reloadLaunchAgent(lc producer.Launchctl, uid int, plistPath string) error {
	domain := fmt.Sprintf("gui/%d", uid)
	target := fmt.Sprintf("%s/%s", domain, launchAgentLabel)
	producer.BootoutCLIAgent(lc, target, plistPath)
	out, err := lc("bootstrap", domain, plistPath)
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %v\nOutput: %s", err, out)
	}
	return nil
}

type hookEvent struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Async   bool   `json:"async,omitempty"`
}

func mergeSettingsJSON(home, binPath string) error {
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return err
	}
	root := map[string]any{}
	existing, err := os.ReadFile(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return fmt.Errorf("settings.json is not valid JSON (comments/trailing-commas not supported): %w", err)
		}
		bak := fmt.Sprintf("%s.bak.%d", settingsPath, os.Getpid())
		if err := os.WriteFile(bak, existing, 0o600); err != nil {
			return err
		}
	}

	hooksRoot, _ := root["hooks"].(map[string]any)
	if hooksRoot == nil {
		hooksRoot = map[string]any{}
	}

	// With the Ember plugin enabled the plugin owns the hooks; registering them
	// here too would run each one twice (one POST per copy).
	stripProducerHooks(hooksRoot)
	var entries []producerHookEntry
	if !pluginEnabled(root) {
		entries = producerHookEntries(binPath)
	}
	for _, ev := range entries {
		filtered, _ := hooksRoot[ev.event].([]any)
		marshalled, _ := json.Marshal(hookEvent{
			Matcher: ev.matcher,
			Hooks: []hookCommand{{
				Type:    "command",
				Command: ev.command,
				Async:   ev.async,
			}},
		})
		var asAny any
		_ = json.Unmarshal(marshalled, &asAny)
		filtered = append(filtered, asAny)
		hooksRoot[ev.event] = filtered
	}
	if len(hooksRoot) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooksRoot
	}

	if sl, ok := root["statusLine"]; ok && !statusLineIsOurs(sl) {
		if raw, err := json.Marshal(sl); err == nil {
			_ = os.WriteFile(wrappedStatuslinePath(home), raw, 0o600)
		}
	}
	root["statusLine"] = map[string]any{
		"type":    "command",
		"command": ourStatuslineCommand(binPath),
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(settingsPath), "settings.tmp-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), settingsPath)
}

type producerHookEntry struct {
	event   string
	matcher string
	command string
	async   bool
}

// producerHookSpec is one hook the producer registers. The settings.json
// installer and the Claude Code plugin (producers/claude-code/plugin) both
// register exactly this list; plugin_test.go keeps hooks.json in sync.
type producerHookSpec struct {
	event      string
	subcommand string
	matcher    string
	async      bool
}

var producerHookSpecs = []producerHookSpec{
	{event: "SessionStart", subcommand: "session-start"},
	{event: "UserPromptSubmit", subcommand: "user-prompt-submit"},
	{event: "PreToolUse", subcommand: "pre-tool-use"},
	{event: "PermissionRequest", subcommand: "permission-request"},
	{event: "PostToolUse", subcommand: "post-tool-use", async: true},
	{event: "PostToolUseFailure", subcommand: "post-tool-use-failure", async: true},
	{event: "PermissionDenied", subcommand: "permission-denied", async: true},
	{event: "Notification", subcommand: "notification", matcher: "permission_prompt|agent_needs_input|agent_completed"},
	{event: "Stop", subcommand: "stop"},
	{event: "StopFailure", subcommand: "stop-failure"},
	{event: "SessionEnd", subcommand: "session-end", matcher: "logout|prompt_input_exit|other|clear|resume"},
}

func producerHookEntries(binPath string) []producerHookEntry {
	logRedirect := ` >>$HOME/Library/Logs/ember-claude-producer.log 2>&1`
	cmd := func(eventName string) string {
		inner := `"` + binPath + `" hook ` + eventName + logRedirect
		return `[ -x "` + binPath + `" ] && ` + inner + ` || true`
	}
	out := make([]producerHookEntry, 0, len(producerHookSpecs))
	for _, s := range producerHookSpecs {
		out = append(out, producerHookEntry{event: s.event, matcher: s.matcher, command: cmd(s.subcommand), async: s.async})
	}
	return out
}

func entryMatchesProducer(e any) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := m["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hm["command"].(string)
		if strings.Contains(cmd, producerName) || strings.Contains(cmd, legacyProducerName) {
			return true
		}
	}
	return false
}
