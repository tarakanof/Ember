package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

const launchAgentLabel = "com.ember.heartbeat"

const (
	producerName       = "ember-claude-producer"
	legacyProducerName = "awtrix-claude-producer"
)

func runInstall(args []string) {
	if err := install(); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		os.Exit(1)
	}
	printPluginNote()
	fmt.Println("Install complete. Edit ~/.config/ember/producer.env if needed, then restart `claude`.")
	printSetupHints(args, true)
}

func runConfigure(args []string) {
	if err := configure(); err != nil {
		fmt.Fprintln(os.Stderr, "configure failed:", err)
		os.Exit(1)
	}
	printPluginNote()
	fmt.Println("Configure complete. Edit ~/.config/ember/producer.env if needed, then restart `claude`.")
	printSetupHints(args, false)
}

func install() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	uid := os.Getuid()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if runtime.GOOS == "darwin" {
		if err := producer.CheckInstallAllowed(producer.ExecLaunchctl, uid, launchAgentLabel, plistPath); err != nil {
			return err
		}
	}
	if err := configure(); err != nil {
		return err
	}
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		plistData, err := generatePlist(binPath, home, uid)
		if err != nil {
			return err
		}
		if err := os.WriteFile(plistPath, plistData, 0o644); err != nil {
			return err
		}
		return reloadLaunchAgent(producer.ExecLaunchctl, uid, plistPath)
	case "linux":
		return producer.InstallUserUnit(producer.ExecRunner, home, userUnit(binPath))
	default:
		return fmt.Errorf("no background service support on %s: run `%s run` under your own supervisor", runtime.GOOS, binPath)
	}
}

const systemdUnitName = "ember-claude-producer"

// userUnit is the systemd --user counterpart of the com.ember.heartbeat LaunchAgent.
func userUnit(binPath string) producer.UserUnit {
	return producer.NewUserUnit(systemdUnitName, "Ember Claude Code heartbeat producer (session heartbeats + usage)", binPath, "run")
}

// printSetupHints prints the post-setup checklist; only a headless install
// touches the network (configure is what Ember.app runs: keep it offline).
func printSetupHints(args []string, installing bool) {
	cfg, err := loadConfig()
	if err != nil {
		return
	}
	home, _ := os.UserHomeDir()
	headless := producer.Headless(args, home)
	lingerUser := ""
	if installing {
		lingerUser = producer.CurrentUser()
	}
	producer.PrintSetupHints(os.Stdout, producer.SetupHintsInput{
		Source: cfg.Source, Token: cfg.Token, Configured: cfg.ServerConfigured, Prefer: cfg.ServerInstance,
		Home: home, Headless: headless, Discover: installing && headless, LingerUser: lingerUser,
	})
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
	if _, _, err := producer.EnsureSourceInEnv(envPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not default EMBER_SOURCE:", err)
	}
	if err := mergeSettingsJSON(home, binPath); err != nil {
		return err
	}
	removeSpikeLog(home)
	return enableHooks(home)
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
	dirs := []string{
		filepath.Join(home, ".config", "ember"),
		filepath.Join(home, ".local", "state", "ember", "sessions"),
		producer.LogDir(home),
	}
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, filepath.Join(home, "Library", "LaunchAgents"))
	}
	for _, d := range dirs {
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
	Timeout int    `json:"timeout,omitempty"`
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
				Timeout: ev.timeout,
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
	timeout int
}

// producerHookSpec is one hook the producer registers. The settings.json
// installer and the Claude Code plugin (producers/claude-code/plugin) both
// register exactly this list; plugin_test.go keeps hooks.json in sync.
type producerHookSpec struct {
	event      string
	subcommand string
	matcher    string
	async      bool
	// timeout (seconds) caps a blocking hook so a wedged producer can't
	// stall a session for Claude Code's 600 s default. Async hooks get none:
	// Claude Code doesn't enforce it on them.
	timeout int
}

// notificationMatcher lists the Notification types the producer maps to a state.
const notificationMatcher = "permission_prompt|idle_prompt|elicitation_dialog|elicitation_url_dialog|" +
	"elicitation_complete|elicitation_response|agent_needs_input|agent_completed|" +
	"quota_auto_resume_fired|quota_auto_resume_stale|quota_auto_resume_disabled"

var producerHookSpecs = []producerHookSpec{
	{event: "SessionStart", subcommand: "session-start", timeout: 5},
	{event: "UserPromptSubmit", subcommand: "user-prompt-submit", timeout: 5},
	{event: "PreToolUse", subcommand: "pre-tool-use", timeout: 5},
	{event: "PermissionRequest", subcommand: "permission-request", timeout: 5},
	{event: "PostToolUse", subcommand: "post-tool-use", async: true},
	{event: "PostToolUseFailure", subcommand: "post-tool-use-failure", async: true},
	{event: "PermissionDenied", subcommand: "permission-denied", async: true},
	{event: "Notification", subcommand: "notification", matcher: notificationMatcher, timeout: 5},
	{event: "Stop", subcommand: "stop", timeout: 5},
	{event: "StopFailure", subcommand: "stop-failure", timeout: 5},
	// SessionEnd hooks share a 1.5 s budget, which a settings-hook timeout
	// raises to match; 2 s covers the producer's own ~1 s cap without making
	// exit wait long on a wedged hook.
	{event: "SessionEnd", subcommand: "session-end", matcher: "logout|prompt_input_exit|other|clear|resume", timeout: 2},
}

func producerHookEntries(binPath string) []producerHookEntry {
	logRedirect := ` >>` + producer.LogDirShell() + `/ember-claude-producer.log 2>&1`
	cmd := func(eventName string) string {
		inner := `"` + binPath + `" hook ` + eventName + logRedirect
		return `[ -x "` + binPath + `" ] && ` + inner + ` || true`
	}
	out := make([]producerHookEntry, 0, len(producerHookSpecs))
	for _, s := range producerHookSpecs {
		out = append(out, producerHookEntry{event: s.event, matcher: s.matcher, command: cmd(s.subcommand), async: s.async, timeout: s.timeout})
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
