package producer

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// The CLI `install`/`uninstall` subcommands and Ember.app's SMAppService
// registration share one launchd label per producer (com.ember.heartbeat,
// com.ember.codex). A blind `launchctl bootout gui/<uid>/<label>` from the CLI
// therefore also removes the app's job, and launchd drops a booted-out
// submitted job for good while Background Items still shows it enabled — the
// daemon then stays dead until the next login (issue #142; a `go test` run of
// the uninstall test did exactly that on a dev Mac). These helpers let the CLI
// tell the two apart and leave the app's job alone.

// Launchctl runs launchctl with args and returns its combined output. It is a
// parameter so tests never touch the real launchd.
type Launchctl func(args ...string) ([]byte, error)

// ExecLaunchctl is the real Launchctl.
func ExecLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

// ErrAppManaged means the label is loaded as Ember.app's SMAppService job.
var ErrAppManaged = errors.New("managed by Ember.app")

// appManaged reports whether `launchctl print` output describes a job that
// SMAppService submitted (i.e. Ember.app registered it).
func appManaged(printOut []byte) bool {
	return strings.Contains(string(printOut), "managed_by = com.apple.xpc.ServiceManagement")
}

// CheckNotAppManaged returns ErrAppManaged (wrapped with the target) when
// target (gui/<uid>/<label>) is loaded as Ember.app's job, nil otherwise.
func CheckNotAppManaged(lc Launchctl, target string) error {
	out, err := lc("print", target)
	if err == nil && appManaged(out) {
		return fmt.Errorf("%s is %w; turn reporting on or off in Ember › Settings › Agents instead", target, ErrAppManaged)
	}
	return nil
}

// BootoutLegacyAgent boots out target only when it is loaded and was not
// submitted by Ember.app. It reports whether it booted anything out.
func BootoutLegacyAgent(lc Launchctl, target string) bool {
	out, err := lc("print", target)
	if err != nil || appManaged(out) {
		return false
	}
	_, _ = lc("bootout", target)
	return true
}
