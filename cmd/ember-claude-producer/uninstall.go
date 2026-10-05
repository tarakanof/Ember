package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func runUninstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: cannot find home dir:", err)
		os.Exit(1)
	}
	if err := deconfigureAt(home); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: settings.json:", err)
	}
	if err := service.Uninstall(home, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall:", err)
	}
	warnPluginStillEnabled()
	fmt.Println("Uninstall complete.")
	fmt.Println("Note: ~/.config/ember/producer.env and")
	fmt.Println("~/.local/state/ember/ were left in place. rm them yourself if desired.")
}

func runDeconfigure() {
	if err := deconfigure(); err != nil {
		fmt.Fprintln(os.Stderr, "deconfigure failed:", err)
		os.Exit(1)
	}
	warnPluginStillEnabled()
	fmt.Println("Deconfigure complete.")
}

func deconfigureAt(home string) error {
	removeSpikeLog(home)
	if err := disableHooks(home); err != nil {
		return err
	}
	return uninstallSettings(home)
}

func deconfigure() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return deconfigureAt(home)
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
		stripProducerHooks(hooksRoot)
		if len(hooksRoot) == 0 {
			delete(root, "hooks")
		}
	}

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

	return saveSettings(settingsPath, body, root, true)
}
