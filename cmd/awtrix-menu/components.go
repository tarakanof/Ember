package main

import (
	"os"
	"path/filepath"
	"regexp"
)

// component identifies one managed LaunchAgent. binary is the producer binary
// name to delegate install/uninstall to; "" means the menu app itself
// (installed in-process, no Uninstall button).
type component struct {
	label  string
	binary string
}

var managedComponents = []component{
	{label: "com.awtrix-ai-status.menu", binary: ""},
	{label: "com.awtrix-ai-status.heartbeat", binary: "awtrix-claude-producer"},
	{label: "com.awtrix-ai-status.codex", binary: "awtrix-codex-producer"},
}

// componentState is the detected state of a component's LaunchAgent.
type componentState struct {
	Installed bool // plist file present
	Disabled  bool // explicitly disabled in the launchd overrides
	Loaded    bool // a job is bootstrapped (launchctl print exits 0)
}

// launchAtLogin reports whether the component will start at next login.
func (s componentState) launchAtLogin() bool { return s.Installed && !s.Disabled }

// resolveBinary locates a producer binary by name: PATH, then ~/go/bin, then the
// directory of the running menu binary. Returns "" when not found. lookPath is
// injected for testability (pass exec.LookPath in production).
func resolveBinary(name string, lookPath func(string) (string, error), home, selfDir string) string {
	if p, err := lookPath(name); err == nil && p != "" {
		return p
	}
	for _, c := range []string{
		filepath.Join(home, "go", "bin", name),
		filepath.Join(selfDir, name),
	} {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

var disabledLineRe = regexp.MustCompile(`"([^"]+)"\s*=>\s*(enabled|disabled)`)

// parseDisabled parses `launchctl print-disabled gui/$UID` output into
// label -> isDisabled. Labels absent from the output are not present in the map
// (callers read a missing key as false = not disabled).
func parseDisabled(out string) map[string]bool {
	m := map[string]bool{}
	for _, match := range disabledLineRe.FindAllStringSubmatch(out, -1) {
		m[match[1]] = match[2] == "disabled"
	}
	return m
}

// stateLabel is the human-readable status shown next to the checkbox.
func (s componentState) stateLabel() string {
	switch {
	case !s.Installed:
		return "Not installed"
	case s.Loaded:
		return "Running"
	default:
		return "Disabled"
	}
}
