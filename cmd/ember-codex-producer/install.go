package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

const launchAgentLabel = "com.ember.codex"

func runInstall(args []string) {
	if err := install(); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		os.Exit(1)
	}
	fmt.Println("Install complete. The Codex producer daemon is now running.")
	printSetupHints(args)
}

func runConfigure(args []string) {
	if err := configure(); err != nil {
		fmt.Fprintln(os.Stderr, "configure failed:", err)
		os.Exit(1)
	}
	fmt.Println("Configure complete.")
	printSetupHints(args)
}

func install() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if runtime.GOOS == "darwin" {
		if err := producer.CheckInstallAllowed(producer.ExecLaunchctl, os.Getuid(), launchAgentLabel, plistPath); err != nil {
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
		_ = exec.Command("xattr", "-d", "com.apple.quarantine", binPath).Run()
		if err := os.WriteFile(plistPath, generatePlist(binPath, home), 0o644); err != nil {
			return err
		}
		return reloadLaunchAgent(producer.ExecLaunchctl, os.Getuid(), plistPath)
	case "linux":
		return producer.InstallUserUnit(producer.ExecRunner, home, userUnit(binPath))
	default:
		return fmt.Errorf("no background service support on %s: run `%s run` under your own supervisor", runtime.GOOS, binPath)
	}
}

// userUnit is the systemd --user counterpart of the com.ember.codex LaunchAgent.
func userUnit(binPath string) producer.UserUnit {
	return producer.UserUnit{Name: systemdUnitName, Description: "Ember Codex producer (tails Codex rollouts, reports status)", ExecStart: []string{binPath, "run"}}
}

const systemdUnitName = "ember-codex-producer"

func configureAt(home string) error {
	for _, d := range []string{
		filepath.Join(home, ".config", "ember"),
		filepath.Join(home, ".local", "state", "ember", "sessions"),
		producer.LogDir(home),
	} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if runtime.GOOS == "darwin" {
		if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o700); err != nil {
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

func printSetupHints(args []string) {
	cfg, err := loadConfig()
	if err != nil {
		return
	}
	home, _ := os.UserHomeDir()
	producer.PrintSetupHints(os.Stdout, producer.SetupHintsInput{
		Source: cfg.Source, Token: cfg.Token, Configured: cfg.ServerConfigured, Prefer: cfg.ServerInstance,
		Home: home, Headless: producer.Headless(args, home),
	})
}
