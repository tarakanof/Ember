package main

import (
	"fmt"
	"os"
	"os/exec"
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

// stateLabel is the human-readable status shown next to the checkbox. It
// encodes BOTH the launch-at-login setting (On/Off) and whether the process is
// running now, so toggling the checkbox produces a visible change even though
// disabling is non-destructive (the process keeps running this session).
func (s componentState) stateLabel() string {
	if !s.Installed {
		return "Not installed"
	}
	onoff := "Off"
	if s.launchAtLogin() {
		onoff = "On"
	}
	run := "stopped"
	if s.Loaded {
		run = "running"
	}
	return onoff + " · " + run
}

func agentPlistPath(home, label string) string {
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func guiTarget(uid int, label string) string { return fmt.Sprintf("gui/%d/%s", uid, label) }
func guiDomain(uid int) string               { return fmt.Sprintf("gui/%d", uid) }

// detectState reads the live state of a component from the filesystem + launchd.
func detectState(c component, home string, uid int) componentState {
	st := componentState{}
	if _, err := os.Stat(agentPlistPath(home, c.label)); err == nil {
		st.Installed = true
	}
	if out, err := exec.Command("launchctl", "print-disabled", guiDomain(uid)).Output(); err == nil {
		st.Disabled = parseDisabled(string(out))[c.label]
	}
	if exec.Command("launchctl", "print", guiTarget(uid, c.label)).Run() == nil {
		st.Loaded = true
	}
	return st
}

// runOp executes one planned op. installSelf calls the menu's own install().
func runOp(o op, uid int) error {
	switch o.kind {
	case opEnable:
		return runCmd("launchctl", "enable", guiTarget(uid, o.label))
	case opDisable:
		return runCmd("launchctl", "disable", guiTarget(uid, o.label))
	case opBootstrap:
		return runCmd("launchctl", "bootstrap", guiDomain(uid), o.plist)
	case opExec:
		return runCmd(o.bin, o.args...)
	case opInstallSelf:
		return install()
	}
	return nil
}

func runCmd(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v\n%s", name, err, out)
	}
	return nil
}
