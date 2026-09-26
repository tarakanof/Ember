package producer

import (
	"errors"
	"reflect"
	"testing"
)

const cliPlist = "/Users/x/Library/LaunchAgents/com.ember.heartbeat.plist"

// fakeLaunchctl records calls and answers `print` / `print-disabled`.
type fakeLaunchctl struct {
	printOut    string
	printErr    error
	disabledOut string
	calls       [][]string
}

func (f *fakeLaunchctl) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch args[0] {
	case "print":
		return []byte(f.printOut), f.printErr
	case "print-disabled":
		return []byte(f.disabledOut), nil
	}
	return nil, nil
}

func (f *fakeLaunchctl) bootedOut() bool {
	for _, c := range f.calls {
		if c[0] == "bootout" {
			return true
		}
	}
	return false
}

// Fixtures: real `launchctl print` shapes from macOS 27.
const (
	appManagedPrint = `gui/501/com.ember.heartbeat = {
	active count = 1
	path = (submitted by smd.339)
	type = Submitted
	managed_by = com.apple.xpc.ServiceManagement
	state = running
}`
	// The same job if Apple dropped the managed_by line.
	submittedNoManagedByPrint = `gui/501/com.ember.heartbeat = {
	path = (submitted by smd.339)
	type = Submitted
	state = running
}`
	cliLoadedPrint = `gui/501/com.ember.heartbeat = {
	active count = 1
	path = /Users/x/Library/LaunchAgents/com.ember.heartbeat.plist
	type = LaunchAgent
	state = running
}`
	otherPathPrint = `gui/501/com.ember.heartbeat = {
	path = /Users/someone-else/Library/LaunchAgents/com.ember.heartbeat.plist
	state = running
}`
	notFoundOut = "Bad request.\nCould not find service \"com.ember.heartbeat\" in domain for user gui: 501\n"
	disabledOut = "disabled services = {\n\t\t\"com.ember.heartbeat\" => enabled\n\t\t\"com.ember.codex\" => enabled\n\t}\n"
)

func TestAgentOwner(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		want Owner
	}{
		{"app managed", appManagedPrint, nil, OwnedByOther},
		{"submitted without managed_by", submittedNoManagedByPrint, nil, OwnedByOther},
		{"cli plist", cliLoadedPrint, nil, OwnedByCLI},
		{"other plist path", otherPathPrint, nil, OwnedByOther},
		{"unparseable", "something new and different", nil, OwnedByOther},
		{"not found", notFoundOut, errors.New("exit status 113"), NotLoaded},
		{"launchctl failed otherwise", "Operation not permitted", errors.New("exit status 1"), OwnedByOther},
	}
	for _, c := range cases {
		f := &fakeLaunchctl{printOut: c.out, printErr: c.err}
		if got := AgentOwner(f.run, "gui/501/com.ember.heartbeat", cliPlist); got != c.want {
			t.Errorf("%s: AgentOwner() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBootoutCLIAgentOnlyBootsOutTheCLIJob(t *testing.T) {
	for _, out := range []string{appManagedPrint, submittedNoManagedByPrint, otherPathPrint, "garbage"} {
		f := &fakeLaunchctl{printOut: out}
		if BootoutCLIAgent(f.run, "gui/501/com.ember.heartbeat", cliPlist) || f.bootedOut() {
			t.Errorf("booted out a job that isn't the CLI's: %q", out)
		}
	}
	f := &fakeLaunchctl{printOut: cliLoadedPrint}
	if !BootoutCLIAgent(f.run, "gui/501/com.ember.heartbeat", cliPlist) {
		t.Error("BootoutCLIAgent() = false for the CLI's job, want true")
	}
	want := [][]string{{"print", "gui/501/com.ember.heartbeat"}, {"bootout", "gui/501/com.ember.heartbeat"}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
}

func TestCheckInstallAllowed(t *testing.T) {
	cases := []struct {
		name     string
		f        *fakeLaunchctl
		wantDeny bool
	}{
		{"app managed", &fakeLaunchctl{printOut: appManagedPrint}, true},
		{"unidentifiable", &fakeLaunchctl{printOut: "garbage"}, true},
		{"app registered but dropped (#142)", &fakeLaunchctl{printOut: notFoundOut, printErr: errors.New("113"), disabledOut: disabledOut}, true},
		{"cli reinstall", &fakeLaunchctl{printOut: cliLoadedPrint}, false},
		{"fresh mac", &fakeLaunchctl{printOut: notFoundOut, printErr: errors.New("113")}, false},
	}
	for _, c := range cases {
		err := CheckInstallAllowed(c.f.run, 501, "com.ember.heartbeat", cliPlist)
		if denied := errors.Is(err, ErrAppManaged); denied != c.wantDeny {
			t.Errorf("%s: CheckInstallAllowed() = %v, want denied=%v", c.name, err, c.wantDeny)
		}
		if c.f.bootedOut() {
			t.Errorf("%s: CheckInstallAllowed booted something out", c.name)
		}
	}
}

func TestAppRegisteredReadsTheEnabledOverride(t *testing.T) {
	f := &fakeLaunchctl{disabledOut: disabledOut}
	if !AppRegistered(f.run, "gui/501", "com.ember.codex") {
		t.Error("AppRegistered() = false with an enabled override")
	}
	f = &fakeLaunchctl{disabledOut: "disabled services = {\n}\n"}
	if AppRegistered(f.run, "gui/501", "com.ember.codex") {
		t.Error("AppRegistered() = true without an override")
	}
}
