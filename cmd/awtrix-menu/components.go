package main

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
