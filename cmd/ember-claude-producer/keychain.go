package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
)

type claudeCreds struct {
	AccessToken string
	ExpiresAtMs int64
}

// parseClaudeCreds reads the claudeAiOauth blob (Keychain item or file).
func parseClaudeCreds(b []byte) (claudeCreds, error) {
	var doc struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return claudeCreds{}, err
	}
	return claudeCreds{AccessToken: doc.OAuth.AccessToken, ExpiresAtMs: doc.OAuth.ExpiresAt}, nil
}

// readClaudeCreds returns the OAuth creds: macOS login Keychain first
// (item "Claude Code-credentials"), then ~/.claude/.credentials.json (Linux).
// Read-only; never refreshes (rotation races the Claude Code daemon).
func readClaudeCreds() (claudeCreds, error) {
	acct := ""
	if u, err := user.Current(); err == nil {
		acct = u.Username
	}
	if out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-a", acct, "-w").Output(); err == nil && len(out) > 0 {
		return parseClaudeCreds(out)
	}
	home, _ := os.UserHomeDir()
	if b, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); err == nil {
		return parseClaudeCreds(b)
	}
	return claudeCreds{}, os.ErrNotExist
}
