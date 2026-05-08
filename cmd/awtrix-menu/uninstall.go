package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func runUninstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: no home dir:", err)
		os.Exit(1)
	}
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, menuLabel)
	plistPath := filepath.Join(home, "Library", "LaunchAgents", menuLabel+".plist")
	bin, _ := os.Executable()

	// 1. bootout (tolerate not-loaded)
	_ = exec.Command("launchctl", "bootout", target).Run()
	// 2. wait briefly for the process to exit
	time.Sleep(500 * time.Millisecond)
	// 3. remove plist
	_ = os.Remove(plistPath)
	// 4. remove binary (best effort; bin path is stable since we just executed it)
	if bin != "" {
		_ = os.Remove(bin)
	}
	fmt.Println("Uninstall complete.")
}
