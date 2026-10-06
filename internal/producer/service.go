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

type Service struct {
	Label        string
	Unit         string
	Description  string
	BrewPATH     bool
	Unquarantine bool
}

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

func (s Service) UserUnit(binPath string) UserUnit {
	return NewUserUnit(s.Unit, s.Description, binPath, "run")
}

func (s Service) CheckInstall(home string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return CheckInstallAllowed(ExecLaunchctl, os.Getuid(), s.Label, s.PlistPath(home))
}

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

func (s Service) Reload(lc Launchctl, uid int, plistPath string) error {
	domain := fmt.Sprintf("gui/%d", uid)
	BootoutCLIAgent(lc, domain+"/"+s.Label, plistPath)
	if out, err := lc("bootstrap", domain, plistPath); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v\nOutput: %s", err, out)
	}
	return nil
}

func (s Service) Uninstall(home string, warn io.Writer) error {
	if runtime.GOOS == "linux" {
		return UninstallUserUnit(ExecRunner, home, s.Unit)
	}
	return s.UninstallLaunchAgent(ExecLaunchctl, home, os.Getuid(), warn)
}

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

func ShellSafePath(p string) bool {
	return !strings.ContainsAny(p, " \t\"'\\$`;|&><*?(){}!#\n")
}

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

func DoctorPrelude(home string) {
	if home != "" {
		_, _, _ = EnsureSourceInEnv(EnvFilePath(home))
	}
}

func EnvFileLine(home string) string {
	envPath := EnvFilePath(home)
	if info, err := os.Stat(envPath); err == nil {
		return fmt.Sprintf("producer.env: %s mode=%#o", envPath, info.Mode().Perm())
	}
	return "producer.env: MISSING at " + envPath
}

func ServerLines(ctx context.Context, home string, c Common) []string {
	lines := ServerReport(ctx, ServerReportInput{Configured: c.ServerConfigured, Prefer: c.ServerInstance, Home: home})
	if h := TokenHint(c.Token); h != "" {
		lines = append(lines, "WARNING: "+h)
	}
	return lines
}
