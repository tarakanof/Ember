package main

import (
	"bytes"
	"encoding/xml"
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

	// Capture both possible binary paths BEFORE removing the plist:
	//  - os.Executable() (the running binary; spec's self-uninstall variant)
	//  - the path recorded in the plist's ProgramArguments[0] (in case GOBIN shifted)
	selfBin, _ := os.Executable()
	plistBin := readPlistBinary(plistPath)

	// 1. bootout (tolerate not-loaded)
	_ = exec.Command("launchctl", "bootout", target).Run()
	// 2. wait up to 2s for the process to exit (pgrep poll)
	waitForProcessExit(menuLabel, 2*time.Second)
	// 3. remove plist
	_ = os.Remove(plistPath)
	// 4. remove binary(ies) — both selfBin and plistBin if they differ.
	for _, p := range distinctNonEmpty(selfBin, plistBin) {
		_ = os.Remove(p)
	}
	fmt.Println("Uninstall complete.")
}

// waitForProcessExit polls `pgrep -f <pattern>` until either no match or the
// timeout elapses. Spec contract: best-effort, never blocks beyond timeout.
func waitForProcessExit(pattern string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// pgrep exits 1 when no match — that's our "process is gone" signal.
		err := exec.Command("pgrep", "-f", pattern).Run()
		if err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// readPlistBinary parses ProgramArguments[0] from the plist at path.
// Returns "" if the file is missing or unparseable; caller must tolerate that.
func readPlistBinary(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// Strip the DOCTYPE so encoding/xml doesn't try to fetch it.
	if i := bytes.Index(data, []byte("<plist")); i > 0 {
		data = data[i:]
	}
	type plistDict struct {
		XMLName xml.Name `xml:"plist"`
		Dict    struct {
			Keys   []string `xml:"key"`
			Values []struct {
				XMLName xml.Name
				Strings []string `xml:"string"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	var pl plistDict
	if err := xml.Unmarshal(data, &pl); err != nil {
		return ""
	}
	// Walk keys, find ProgramArguments, return first <string> child.
	// Note: encoding/xml flattens our "any" array such that index alignment with
	// keys is reliable for top-level dict entries.
	for i, k := range pl.Dict.Keys {
		if k != "ProgramArguments" {
			continue
		}
		if i >= len(pl.Dict.Values) {
			return ""
		}
		// ProgramArguments is the i-th value child. Find its <array><string>…
		v := pl.Dict.Values[i]
		if v.XMLName.Local != "array" {
			return ""
		}
		if len(v.Strings) == 0 {
			return ""
		}
		return v.Strings[0]
	}
	return ""
}

// distinctNonEmpty returns the input strings with empties removed and dups
// dropped (preserving first-seen order).
func distinctNonEmpty(in ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
