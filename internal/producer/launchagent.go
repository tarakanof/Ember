package producer

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type Launchctl func(args ...string) ([]byte, error)

func ExecLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

// ErrAppManaged: the label may belong to Ember.app's SMAppService, so the CLI must leave it alone.
var ErrAppManaged = errors.New("managed by Ember.app")

type Owner int

const (
	NotLoaded Owner = iota
	OwnedByCLI
	OwnedByOther
)

const launchctlNotFound = 113

func notFound(out []byte, err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == launchctlNotFound {
		return true
	}
	return strings.Contains(string(out), "Could not find service")
}

func printField(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+" = "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

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

func AppRegistered(lc Launchctl, domain, label string) bool {
	out, err := lc("print-disabled", domain)
	if err != nil {
		return false
	}
	return strings.Contains(string(out), fmt.Sprintf("%q => enabled", label))
}

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

func BootoutCLIAgent(lc Launchctl, target, cliPlist string) bool {
	if AgentOwner(lc, target, cliPlist) != OwnedByCLI {
		return false
	}
	_, _ = lc("bootout", target)
	return true
}
