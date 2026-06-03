package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func runUninstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: cannot find home dir:", err)
		os.Exit(1)
	}
	uid := os.Getuid()
	if err := uninstallSettings(home); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: settings.json:", err)
	}
	if err := uninstallPlist(home, uid); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: plist:", err)
	}
	fmt.Println("Uninstall complete.")
	fmt.Println("Note: ~/.config/ember/producer.env and")
	fmt.Println("~/.local/state/ember/ were left in place. rm them yourself if desired.")
}

func uninstallSettings(home string) error {
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	body, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	root := map[string]any{}
	if err := json.Unmarshal(body, &root); err != nil {
		return fmt.Errorf("settings.json invalid JSON: %w", err)
	}
	if hooksRoot, ok := root["hooks"].(map[string]any); ok && hooksRoot != nil {
		for ev, entries := range hooksRoot {
			list, ok := entries.([]any)
			if !ok {
				continue
			}
			filtered := []any{}
			for _, e := range list {
				if !entryMatchesProducer(e) {
					filtered = append(filtered, e)
				}
			}
			if len(filtered) == 0 {
				delete(hooksRoot, ev)
			} else {
				hooksRoot[ev] = filtered
			}
		}
		if len(hooksRoot) == 0 {
			delete(root, "hooks")
		} else {
			root["hooks"] = hooksRoot
		}
	}

	// Restore the user's statusLine verbatim if the slot is currently ours.
	if sl, ok := root["statusLine"]; ok && statusLineIsOurs(sl) {
		wrappedPath := wrappedStatuslinePath(home)
		if raw, err := os.ReadFile(wrappedPath); err == nil {
			var orig any
			if json.Unmarshal(raw, &orig) == nil {
				root["statusLine"] = orig
			} else {
				delete(root, "statusLine")
			}
			_ = os.Remove(wrappedPath)
		} else {
			delete(root, "statusLine")
		}
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	bak := fmt.Sprintf("%s.bak.%d", settingsPath, os.Getpid())
	_ = os.WriteFile(bak, body, 0o600)
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

func uninstallPlist(home string, uid int) error {
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	target := fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)
	_ = exec.Command("launchctl", "bootout", target).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
