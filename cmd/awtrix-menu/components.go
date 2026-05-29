package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// component identifies one managed LaunchAgent. binary is the producer binary
// name whose install/uninstall subcommands drive the checkbox; "" means the
// menu app itself, which is shown read-only (it cannot cleanly install or
// uninstall itself from its own running process).
type component struct {
	label  string
	binary string
}

var managedComponents = []component{
	{label: "com.awtrix-ai-status.menu", binary: ""},
	{label: "com.awtrix-ai-status.heartbeat", binary: "awtrix-claude-producer"},
	{label: "com.awtrix-ai-status.codex", binary: "awtrix-codex-producer"},
}

// componentState is the detected state of a component's LaunchAgent. The
// checkbox represents "installed": an installed agent has RunAtLoad=true so it
// launches at login (and is registered in macOS Login Items / Background Task
// Management). Installing/uninstalling is therefore what the user sees change
// in System Settings — there is no separate enable/disable layer.
type componentState struct {
	Installed bool // plist file present
	Loaded    bool // a job is bootstrapped (launchctl print exits 0)
}

// launchAtLogin reports whether the component launches at login — true exactly
// when it is installed (the plist carries RunAtLoad).
func (s componentState) launchAtLogin() bool { return s.Installed }

// stateLabel is the human-readable status shown next to the row. It encodes the
// launch-at-login setting (On = installed) plus whether the process is running.
func (s componentState) stateLabel() string {
	if !s.Installed {
		return "Not installed"
	}
	if s.Loaded {
		return "On · running"
	}
	return "On · stopped"
}

// op is a producer subcommand to run (install or uninstall).
type op struct {
	bin  string
	args []string
}

// planToggle returns the op to move producer c to the desired installed state
// want. Installing makes it launch at login (and appear in System Settings);
// uninstalling removes it. Returns nil when no change is needed, or for the
// menu app (binary == ""), which is read-only.
func planToggle(c component, st componentState, want bool, binPath string) []op {
	if c.binary == "" {
		return nil
	}
	if want && !st.Installed {
		return []op{{bin: binPath, args: []string{"install"}}}
	}
	if !want && st.Installed {
		return []op{{bin: binPath, args: []string{"uninstall"}}}
	}
	return nil
}

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

func agentPlistPath(home, label string) string {
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func guiTarget(uid int, label string) string { return fmt.Sprintf("gui/%d/%s", uid, label) }

// detectState reads the live state of a component from the filesystem + launchd.
func detectState(c component, home string, uid int) componentState {
	st := componentState{}
	if _, err := os.Stat(agentPlistPath(home, c.label)); err == nil {
		st.Installed = true
	}
	if exec.Command("launchctl", "print", guiTarget(uid, c.label)).Run() == nil {
		st.Loaded = true
	}
	return st
}

// runOp executes one planned producer subcommand (install/uninstall).
func runOp(o op) error { return runCmd(o.bin, o.args...) }

func runCmd(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v\n%s", name, err, out)
	}
	return nil
}
