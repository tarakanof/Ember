package producer

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Service is a producer's background daemon (`<bin> run`): a LaunchAgent on
// macOS, a systemd --user unit on Linux.
type Service struct {
	Label       string // LaunchAgent label, e.g. "com.ember.codex"
	Unit        string // systemd unit name, e.g. "ember-codex-producer"
	Description string // systemd Description
	// BrewPATH sets the LaunchAgent's PATH to include Homebrew, for daemons
	// that run other CLIs (claude, codex).
	BrewPATH bool
	// Unquarantine strips com.apple.quarantine from the binary before
	// loading it (a downloaded release would otherwise be blocked).
	Unquarantine bool
}

// PlistPath is the CLI's LaunchAgent plist in ~/Library/LaunchAgents.
func (s Service) PlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", s.Label+".plist")
}

const plistHead = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>run</string>
    </array>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>Nice</key>
    <integer>10</integer>
    <key>LowPriorityIO</key>
    <true/>
`

const plistBrewPATH = `    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    </dict>
`

const plistTail = `</dict>
</plist>
`

// Plist is the LaunchAgent running `binPath run`, kept alive by launchd.
// The daemon opens its own log, so no StandardOutPath is set.
func (s Service) Plist(binPath string) []byte {
	out := fmt.Sprintf(plistHead, xmlEscape(s.Label), xmlEscape(binPath))
	if s.BrewPATH {
		out += plistBrewPATH
	}
	return []byte(out + plistTail)
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// UserUnit is the systemd --user counterpart of the LaunchAgent.
func (s Service) UserUnit(binPath string) UserUnit {
	return NewUserUnit(s.Unit, s.Description, binPath, "run")
}

// CheckInstall refuses on macOS when Ember.app owns the LaunchAgent label.
func (s Service) CheckInstall(home string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return CheckInstallAllowed(ExecLaunchctl, os.Getuid(), s.Label, s.PlistPath(home))
}

// Install writes and (re)starts the service for binPath.
func (s Service) Install(home, binPath string) error {
	switch runtime.GOOS {
	case "darwin":
		if s.Unquarantine {
			_ = exec.Command("xattr", "-d", "com.apple.quarantine", binPath).Run()
		}
		plistPath := s.PlistPath(home)
		if err := os.WriteFile(plistPath, s.Plist(binPath), 0o644); err != nil {
			return err
		}
		return s.Reload(ExecLaunchctl, os.Getuid(), plistPath)
	case "linux":
		return InstallUserUnit(ExecRunner, home, s.UserUnit(binPath))
	default:
		return fmt.Errorf("no background service support on %s: run `%s run` under your own supervisor", runtime.GOOS, binPath)
	}
}

// Reload boots out the CLI's own copy of the job (never Ember.app's) and
// bootstraps plistPath.
func (s Service) Reload(lc Launchctl, uid int, plistPath string) error {
	domain := fmt.Sprintf("gui/%d", uid)
	BootoutCLIAgent(lc, domain+"/"+s.Label, plistPath)
	if out, err := lc("bootstrap", domain, plistPath); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v\nOutput: %s", err, out)
	}
	return nil
}

// Uninstall stops and removes the service. Ember.app's job is left loaded
// with a note on warn; the CLI's plist is removed either way.
func (s Service) Uninstall(home string, warn io.Writer) error {
	if runtime.GOOS == "linux" {
		return UninstallUserUnit(ExecRunner, home, s.Unit)
	}
	return s.UninstallLaunchAgent(ExecLaunchctl, home, os.Getuid(), warn)
}

// UninstallLaunchAgent is Uninstall's macOS half.
func (s Service) UninstallLaunchAgent(lc Launchctl, home string, uid int, warn io.Writer) error {
	plistPath := s.PlistPath(home)
	target := fmt.Sprintf("gui/%d/%s", uid, s.Label)
	switch AgentOwner(lc, target, plistPath) {
	case OwnedByCLI:
		_, _ = lc("bootout", target)
	case OwnedByOther:
		fmt.Fprintf(warn, "uninstall: left %s loaded: it's Ember.app's (turn reporting off in Ember › Settings › Agents)\n", target)
	case NotLoaded:
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Status is doctor's service lines: the systemd unit state on Linux,
// whether the LaunchAgent plist is installed elsewhere.
func (s Service) Status(home string) []string {
	if runtime.GOOS == "linux" {
		return UserUnitStatus(ExecRunner, home, s.Unit, CurrentUser())
	}
	plistPath := s.PlistPath(home)
	if _, err := os.Stat(plistPath); err == nil {
		return []string{"LaunchAgent: installed at " + plistPath}
	}
	return []string{"LaunchAgent: NOT installed"}
}

// Configure is the file-only setup every producer shares: the config, state
// and log dirs (plus ~/Library/LaunchAgents on macOS), and producer.env from
// the template when missing, with EMBER_SOURCE defaulted.
func Configure(home string) error {
	dirs := []string{
		filepath.Join(home, ".config", "ember"),
		filepath.Join(home, ".local", "state", "ember", "sessions"),
		LogDir(home),
	}
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, filepath.Join(home, "Library", "LaunchAgents"))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	envPath := EnvFilePath(home)
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if err := os.WriteFile(envPath, []byte(EnvExample()), 0o600); err != nil {
			return err
		}
	}
	if _, _, err := EnsureSourceInEnv(envPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not default EMBER_SOURCE:", err)
	}
	return nil
}

// ShellSafePath reports whether p can be pasted unquoted into a shell
// command (hook commands in settings.json).
func ShellSafePath(p string) bool {
	return !strings.ContainsAny(p, " \t\"'\\$`;|&><*?(){}!#\n")
}

// PrintSetupHintsFor prints the post-install/configure checklist for c; only
// a headless install touches the network (configure is what Ember.app runs:
// keep it offline).
func PrintSetupHintsFor(w io.Writer, c Common, args []string, installing bool) {
	home, _ := os.UserHomeDir()
	headless := Headless(args, home)
	lingerUser := ""
	if installing {
		lingerUser = CurrentUser()
	}
	PrintSetupHints(w, SetupHintsInput{
		Source: c.Source, Token: c.Token, Configured: c.ServerConfigured, Prefer: c.ServerInstance,
		Home: home, Headless: headless, Discover: installing && headless, LingerUser: lingerUser,
	})
}

// DoctorPrelude defaults EMBER_SOURCE in producer.env, as install does, so
// doctor reports the value the hooks will use.
func DoctorPrelude(home string) {
	if home != "" {
		_, _, _ = EnsureSourceInEnv(EnvFilePath(home))
	}
}

// EnvFileLine is doctor's producer.env line (path and mode, or MISSING).
func EnvFileLine(home string) string {
	envPath := EnvFilePath(home)
	if info, err := os.Stat(envPath); err == nil {
		return fmt.Sprintf("producer.env: %s mode=%#o", envPath, info.Mode().Perm())
	}
	return "producer.env: MISSING at " + envPath
}

// ServerLines is doctor's server reachability report plus a token warning.
func ServerLines(ctx context.Context, home string, c Common) []string {
	lines := ServerReport(ctx, ServerReportInput{Configured: c.ServerConfigured, Prefer: c.ServerInstance, Home: home})
	if h := TokenHint(c.Token); h != "" {
		lines = append(lines, "WARNING: "+h)
	}
	return lines
}
