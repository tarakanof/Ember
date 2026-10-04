package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tarakanof/ember/internal/producer"
)

func runUninstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: cannot find home dir:", err)
		os.Exit(1)
	}
	uid := os.Getuid()
	if err := deconfigureAt(home); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: settings.json:", err)
	}
	if runtime.GOOS == "linux" {
		if err := producer.UninstallUserUnit(producer.ExecRunner, home, systemdUnitName); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall: systemd unit:", err)
		}
	} else if err := uninstallPlist(producer.ExecLaunchctl, home, uid); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: plist:", err)
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

func uninstallPlist(lc producer.Launchctl, home string, uid int) error {
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	target := fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)
	switch producer.AgentOwner(lc, target, plistPath) {
	case producer.OwnedByCLI:
		_, _ = lc("bootout", target)
	case producer.OwnedByOther:
		fmt.Fprintf(os.Stderr, "uninstall: left %s loaded: it's Ember.app's (turn reporting off in Ember › Settings › Agents)\n", target)
	case producer.NotLoaded:
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
