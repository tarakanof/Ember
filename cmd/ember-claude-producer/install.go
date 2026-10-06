package main

import (
	"encoding/json"
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

var service = producer.Service{
	Label:       launchAgentLabel,
	Unit:        "ember-claude-producer",
	Description: "Ember Claude Code heartbeat producer (session heartbeats + usage)",
	BrewPATH:    true,
}

func install() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := service.CheckInstall(home); err != nil {
		return err
	}
	if err := configure(); err != nil {
		return err
	}
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	return service.Install(home, binPath)
}

func printSetupHints(args []string, installing bool) {
	if cfg, err := loadConfig(); err == nil {
		producer.PrintSetupHintsFor(os.Stdout, cfg.Common, args, installing)
	}
}

func configureAt(home, binPath string) error {
	if err := producer.Configure(home); err != nil {
		return err
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
	if !producer.ShellSafePath(binPath) {
		return fmt.Errorf("binary path contains shell metacharacters: %s\nMove the binary to a path without spaces or special chars", binPath)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return configureAt(home, binPath)
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
	}

	hooksRoot, _ := root["hooks"].(map[string]any)
	if hooksRoot == nil {
		hooksRoot = map[string]any{}
	}

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

	return saveSettings(settingsPath, existing, root, false)
}

type producerHookEntry struct {
	event   string
	matcher string
	command string
	async   bool
	timeout int
}

type producerHookSpec struct {
	event      string
	subcommand string
	matcher    string
	async      bool
	timeout    int
}

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
