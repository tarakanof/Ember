package producer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeLaunchctl records calls and answers `print` with printOut/printErr.
type fakeLaunchctl struct {
	printOut []byte
	printErr error
	calls    [][]string
}

func (f *fakeLaunchctl) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "print" {
		return f.printOut, f.printErr
	}
	return nil, nil
}

const appManagedPrint = `gui/501/com.ember.heartbeat = {
	active count = 1
	path = (submitted by smd.339)
	type = Submitted
	managed_by = com.apple.xpc.ServiceManagement
	state = running
}`

const cliLoadedPrint = `gui/501/com.ember.heartbeat = {
	active count = 1
	path = /Users/x/Library/LaunchAgents/com.ember.heartbeat.plist
	state = running
}`

func TestBootoutLegacyAgentSkipsAppManagedJob(t *testing.T) {
	f := &fakeLaunchctl{printOut: []byte(appManagedPrint)}
	if booted := BootoutLegacyAgent(f.run, "gui/501/com.ember.heartbeat"); booted {
		t.Error("BootoutLegacyAgent() = true for an SMAppService job, want false")
	}
	for _, c := range f.calls {
		if c[0] == "bootout" {
			t.Fatalf("booted out the app-managed job: calls=%v", f.calls)
		}
	}
}

func TestBootoutLegacyAgentBootsOutCLILoadedJob(t *testing.T) {
	f := &fakeLaunchctl{printOut: []byte(cliLoadedPrint)}
	if booted := BootoutLegacyAgent(f.run, "gui/501/com.ember.heartbeat"); !booted {
		t.Error("BootoutLegacyAgent() = false for a CLI-loaded job, want true")
	}
	want := [][]string{{"print", "gui/501/com.ember.heartbeat"}, {"bootout", "gui/501/com.ember.heartbeat"}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
}

func TestBootoutLegacyAgentSkipsWhenNotLoaded(t *testing.T) {
	f := &fakeLaunchctl{printErr: errors.New("exit status 113")}
	if booted := BootoutLegacyAgent(f.run, "gui/501/com.ember.codex"); booted {
		t.Error("BootoutLegacyAgent() = true for an unloaded job, want false")
	}
	if len(f.calls) != 1 {
		t.Errorf("calls = %v, want only the print probe", f.calls)
	}
}

func TestCheckNotAppManagedRejectsSMAppServiceJob(t *testing.T) {
	f := &fakeLaunchctl{printOut: []byte(appManagedPrint)}
	err := CheckNotAppManaged(f.run, "gui/501/com.ember.heartbeat")
	if !errors.Is(err, ErrAppManaged) {
		t.Fatalf("CheckNotAppManaged() = %v, want ErrAppManaged", err)
	}
	if !strings.Contains(err.Error(), "com.ember.heartbeat") {
		t.Errorf("error %q doesn't name the job", err)
	}
}

func TestCheckNotAppManagedAllowsCLIJob(t *testing.T) {
	f := &fakeLaunchctl{printOut: []byte(cliLoadedPrint)}
	if err := CheckNotAppManaged(f.run, "gui/501/com.ember.heartbeat"); err != nil {
		t.Errorf("CheckNotAppManaged() on a CLI job = %v, want nil", err)
	}
}
