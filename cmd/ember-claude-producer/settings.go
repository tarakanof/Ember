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

// saveSettings saves root as settings.json when it differs from old (the
// file's current bytes). The previous content goes to settings.json.bak.<pid>
// and older backups are removed, so configure runs never pile them up. A
// symlinked settings.json (dotfiles) is written through, keeping the link.
func saveSettings(settingsPath string, old []byte, root map[string]any) error {
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if bytes.Equal(out, old) {
		return nil
	}
	if len(old) > 0 {
		bak := fmt.Sprintf("%s.bak.%d", settingsPath, os.Getpid())
		if err := os.WriteFile(bak, old, 0o600); err != nil {
			return err
		}
		pruneSettingsBackups(settingsPath, bak)
	}
	return producer.WriteFileAtomic(settingsPath, out, 0o600)
}

// pruneSettingsBackups removes every settings.json.bak.<pid> except keep.
func pruneSettingsBackups(settingsPath, keep string) {
	matches, _ := filepath.Glob(settingsPath + ".bak.*")
	for _, m := range matches {
		suffix := strings.TrimPrefix(m, settingsPath+".bak.")
		if _, err := strconv.Atoi(suffix); err != nil || m == keep {
			continue
		}
		_ = os.Remove(m)
	}
}
