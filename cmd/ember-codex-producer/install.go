package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

const launchAgentLabel = "com.ember.codex"

func runInstall() {
	if err := install(); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		os.Exit(1)
	}
	fmt.Println("Install complete. The Codex producer daemon is now running.")
	fmt.Println("Ensure ~/.config/ember/producer.env has EMBER_SOURCE + EMBER_SERVER_URL + EMBER_TOKEN.")
	printSourceHint()
}

func runConfigure() {
	if err := configure(); err != nil {
		fmt.Fprintln(os.Stderr, "configure failed:", err)
		os.Exit(1)
	}
	fmt.Println("Configure complete. Ensure ~/.config/ember/producer.env has EMBER_SOURCE + EMBER_SERVER_URL + EMBER_TOKEN.")
	printSourceHint()
}

func install() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if err := producer.CheckInstallAllowed(producer.ExecLaunchctl, os.Getuid(), launchAgentLabel, plistPath); err != nil {
		return err
	}
	if err := configure(); err != nil {
		return err
	}
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", binPath).Run()

	if err := os.WriteFile(plistPath, generatePlist(binPath, home), 0o644); err != nil {
		return err
	}
	return reloadLaunchAgent(producer.ExecLaunchctl, os.Getuid(), plistPath)
}

func configureAt(home string) error {
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
	envPath := filepath.Join(home, ".config", "ember", "producer.env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if err := os.WriteFile(envPath, []byte(envExample()), 0o600); err != nil {
			return err
		}
	}
	if _, _, err := producer.EnsureSourceInEnv(envPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not default EMBER_SOURCE:", err)
	}
	return nil
}

func configure() error {
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	if !shellSafePath(binPath) {
		return fmt.Errorf("binary path contains shell metacharacters: %s", binPath)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return configureAt(home)
}

func generatePlist(binPath, home string) []byte {
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
	return []byte(fmt.Sprintf(tmpl, xmlEscape(launchAgentLabel), xmlEscape(binPath)))
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
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

func envExample() string {
	return producer.EnvExample()
}

func printSourceHint() {
	if cfg, err := loadConfig(); err == nil {
		fmt.Println(producer.SourceHint(cfg.Source))
	}
}
