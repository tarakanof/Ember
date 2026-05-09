package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const menuLabel = "com.awtrix-ai-status.menu"

func runInstall() {
	if err := install(); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		os.Exit(1)
	}
	fmt.Println("Install complete. The menu-bar icon should appear within a few seconds.")
}

func install() error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	uid := os.Getuid()
	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(home, "Library", "Logs"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "awtrix-ai-status"), 0o700); err != nil {
		return err
	}
	plistPath := filepath.Join(plistDir, menuLabel+".plist")
	plist := generatePlist(bin, home, uid)
	if err := os.WriteFile(plistPath, plist, 0o644); err != nil {
		return err
	}
	// Detect existing instance + bootout it
	target := fmt.Sprintf("gui/%d/%s", uid, menuLabel)
	_ = exec.Command("launchctl", "bootout", target).Run()
	// Wait for the prior instance to exit before bootstrapping the new plist
	// (avoids two menu-bar icons on upgrade/reinstall).
	waitForProcessExit("awtrix-menu run", 2*time.Second)
	// Bootstrap
	out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %v\nOutput: %s", err, out)
	}
	return nil
}

func generatePlist(binPath, home string, uid int) []byte {
	logPath := filepath.Join(home, "Library", "Logs", "awtrix-menu.log")
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
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>LimitLoadToSessionType</key>
    <string>Aqua</string>
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
		xmlEscape(menuLabel),
		xmlEscape(binPath),
		xmlEscape(logPath),
		xmlEscape(logPath),
	)
	return []byte(out)
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
