package producer

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// The CLI `install`/`uninstall` subcommands and Ember.app's SMAppService
// registration share one launchd label per producer (com.ember.heartbeat,
// com.ember.codex). A blind `launchctl bootout gui/<uid>/<label>` from the CLI
// therefore also removes the app's job, and launchd drops a booted-out
// submitted job for good while Background Items still shows it enabled — the
// daemon then stays dead until the next login (issue #142; a `go test` run of
// the uninstall test did exactly that on a dev Mac). These helpers let the CLI
// tell the two apart. They fail closed: the CLI only touches a job it can
// positively identify as its own, because `launchctl print` output is not a
// documented format.

// Launchctl runs launchctl with args and returns its combined output. It is a
// parameter so tests never touch the real launchd.
type Launchctl func(args ...string) ([]byte, error)

// ExecLaunchctl is the real Launchctl.
func ExecLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

// ErrAppManaged means the label belongs (or may belong) to Ember.app's
// SMAppService registration, so the CLI must leave it alone.
var ErrAppManaged = errors.New("managed by Ember.app")

// Owner is who a launchd job belongs to, as far as the CLI can tell.
type Owner int

const (
	// NotLoaded: launchd has no job with the label.
	NotLoaded Owner = iota
	// OwnedByCLI: loaded from the CLI's own plist in ~/Library/LaunchAgents.
	OwnedByCLI
	// OwnedByOther: Ember.app's job, or anything the CLI can't identify.
	OwnedByOther
)

// launchctlNotFound is `launchctl print`'s exit status for a missing service.
const launchctlNotFound = 113

// notFound reports whether a failed `launchctl print` means "no such service"
// (as opposed to launchctl itself failing).
func notFound(out []byte, err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == launchctlNotFound {
		return true
	}
	return strings.Contains(string(out), "Could not find service")
}

// printField returns the value of a tab-indented top-level `key = value` line
// of `launchctl print` output, or "".
func printField(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+" = "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// AgentOwner classifies target (gui/<uid>/<label>). Only a job whose `path`
// is exactly cliPlist and that carries no SMAppService marker is OwnedByCLI;
// an unexpected launchctl failure or unrecognised output is OwnedByOther.
func AgentOwner(lc Launchctl, target, cliPlist string) Owner {
	out, err := lc("print", target)
	if err != nil {
		if notFound(out, err) {
			return NotLoaded
		}
		return OwnedByOther
	}
	s := string(out)
	if strings.Contains(s, "com.apple.xpc.ServiceManagement") ||
		strings.Contains(s, "submitted by smd") ||
		printField(s, "type") == "Submitted" {
		return OwnedByOther
	}
	if p := printField(s, "path"); p != "" && filepath.Clean(p) == filepath.Clean(cliPlist) {
		return OwnedByCLI
	}
	return OwnedByOther
}

// AppRegistered reports whether launchd holds an "enabled" override for label
// in domain (gui/<uid>). smd writes one when Ember.app registers the agent,
// and it survives the job being dropped, so it flags the #142 state where
// Background Items says "on" but nothing is loaded. The CLI's own bootstrap
// never writes one.
func AppRegistered(lc Launchctl, domain, label string) bool {
	out, err := lc("print-disabled", domain)
	if err != nil {
		return false
	}
	return strings.Contains(string(out), fmt.Sprintf("%q => enabled", label))
}

// CheckInstallAllowed returns ErrAppManaged (wrapped with guidance) unless the
// CLI may load its own LaunchAgent for label: the label is either not loaded
// and not registered by the app, or loaded from cliPlist. Call it before
// changing any config.
func CheckInstallAllowed(lc Launchctl, uid int, label, cliPlist string) error {
	domain := fmt.Sprintf("gui/%d", uid)
	target := domain + "/" + label
	switch AgentOwner(lc, target, cliPlist) {
	case OwnedByCLI:
		return nil
	case NotLoaded:
		if !AppRegistered(lc, domain, label) {
			return nil
		}
		return fmt.Errorf("%s is registered by Ember.app but not running (%w); use Ember › Settings › Agents › Repair instead", target, ErrAppManaged)
	default:
		return fmt.Errorf("%s is %w (or couldn't be identified); turn reporting on or off in Ember › Settings › Agents instead", target, ErrAppManaged)
	}
}

// BootoutCLIAgent boots out target only when it is OwnedByCLI (loaded from
// cliPlist). It reports whether it booted anything out.
func BootoutCLIAgent(lc Launchctl, target, cliPlist string) bool {
	if AgentOwner(lc, target, cliPlist) != OwnedByCLI {
		return false
	}
	_, _ = lc("bootout", target)
	return true
}
