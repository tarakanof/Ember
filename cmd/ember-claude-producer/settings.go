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

func settingsBackupPath(settingsPath string) string {
	return settingsPath + ".ember-bak"
}

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
