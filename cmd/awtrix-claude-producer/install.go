package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const launchAgentLabel = "com.awtrix-ai-status.heartbeat"

func runInstall() {
	if err := install(); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		os.Exit(1)
	}
	fmt.Println("Install complete. Edit ~/.config/awtrix-ai-status/producer.env, then restart `claude`.")
}

func install() error {
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
	uid := os.Getuid()
	if err := createInstallDirs(home); err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	plistData, err := generatePlist(binPath, home, uid)
	if err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, plistData, 0o644); err != nil {
		return err
	}
	if err := reloadLaunchAgent(uid, plistPath); err != nil {
		return err
	}
	envPath := filepath.Join(home, ".config", "awtrix-ai-status", "producer.env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if err := os.WriteFile(envPath, []byte(producerEnvExampleContent()), 0o600); err != nil {
			return err
		}
	}
	if err := mergeSettingsJSON(home, binPath); err != nil {
		return err
	}
	return nil
}

func createInstallDirs(home string) error {
	for _, d := range []string{
		filepath.Join(home, ".config", "awtrix-ai-status"),
		filepath.Join(home, ".local", "state", "awtrix-ai-status", "sessions"),
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
	stdoutPath := filepath.Join(home, "Library", "Logs", "awtrix-ai-status-tick.log")
	const tmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>tick</string>
    </array>
    <key>StartInterval</key>
    <integer>10</integer>
    <key>RunAtLoad</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>Nice</key>
    <integer>10</integer>
    <key>LowPriorityIO</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
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
		xmlEscape(stdoutPath),
		xmlEscape(stdoutPath),
	)
	return []byte(out), nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func producerEnvExampleContent() string {
	return `# awtrix-claude-producer configuration
# Required:
STATUS_SOURCE=set-me-to-this-laptop-id
STATUS_SERVER_URL=http://192.168.0.36:8080
STATUS_TOKEN=set-me-to-the-server-bearer-token

# Optional (defaults shown):
# STATUS_HEARTBEAT_TTL_HOURS=6
# STATUS_HOOK_TIMEOUT_MS=500
`
}

// Stubs for Tasks 11 and 12.
func reloadLaunchAgent(uid int, plistPath string) error { return nil }
func mergeSettingsJSON(home, binPath string) error      { return nil }
