package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

// settingsBackupPath is the one backup configure/deconfigure keep: each write
// overwrites it, so backups never pile up. Older producers wrote
// settings.json.bak.<pid> on every run; those are left alone (a hand-made
// settings.json.bak.<date> looks the same) and doctor counts them.
func settingsBackupPath(settingsPath string) string {
	return settingsPath + ".ember-bak"
}

// saveSettings saves root as settings.json when it differs from old (the
// file's current bytes), backing old up to settingsBackupPath first. With
// bestEffortBackup a failed backup only warns (uninstall must still remove
// the hooks); otherwise it aborts. A symlinked settings.json (dotfiles) is
// written through, keeping the link.
func saveSettings(settingsPath string, old []byte, root map[string]any, bestEffortBackup bool) error {
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if bytes.Equal(out, old) {
		return nil
	}
	if len(old) > 0 {
		if err := os.WriteFile(settingsBackupPath(settingsPath), old, 0o600); err != nil {
			if !bestEffortBackup {
				return fmt.Errorf("back up settings.json: %w", err)
			}
			fmt.Fprintln(os.Stderr, "warning: could not back up settings.json:", err)
		}
	}
	return producer.WriteFileAtomic(settingsPath, out, 0o600)
}

// legacySettingsBackups counts settings.json.bak.<number> files, which older
// producers wrote on every configure.
func legacySettingsBackups(settingsPath string) int {
	matches, _ := filepath.Glob(settingsPath + ".bak.*")
	n := 0
	for _, m := range matches {
		if _, err := strconv.Atoi(strings.TrimPrefix(m, settingsPath+".bak.")); err == nil {
			n++
		}
	}
	return n
}
