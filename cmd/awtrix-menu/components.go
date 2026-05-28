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

type opKind int

const (
	opEnable      opKind = iota // launchctl enable <target>
	opDisable                   // launchctl disable <target>
	opBootstrap                 // launchctl bootstrap <domain> <plist>
	opExec                      // run <bin> <args...> (producer install/uninstall)
	opInstallSelf               // run the menu app's own install() in-process
)

type op struct {
	kind  opKind
	label string
	plist string
	bin   string
	args  []string
}

// planToggle returns the ordered ops to move component c from its current state
// st to the desired launch-at-login state want. Returns nil when no change is
// needed. binPath must be a resolved producer binary when an install may be
// required (caller checks resolution first); plist is the component's plist path.
func planToggle(c component, st componentState, want bool, binPath, plist string) []op {
	if want == st.launchAtLogin() {
		return nil
	}
	if want {
		if !st.Installed {
			if c.binary == "" {
				return []op{{kind: opInstallSelf}}
			}
			return []op{{kind: opExec, bin: binPath, args: []string{"install"}}}
		}
		ops := []op{{kind: opEnable, label: c.label}}
		if !st.Loaded {
			ops = append(ops, op{kind: opBootstrap, label: c.label, plist: plist})
		}
		return ops
	}
	if !st.Installed {
		return nil
	}
	return []op{{kind: opDisable, label: c.label}}
}

// planUninstall returns the op to fully remove a producer; nil for the menu app
// (which has no Uninstall control).
func planUninstall(c component, binPath string) []op {
	if c.binary == "" {
		return nil
	}
	return []op{{kind: opExec, bin: binPath, args: []string{"uninstall"}}}
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
