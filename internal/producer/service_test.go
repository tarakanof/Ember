package producer

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServicePlist(t *testing.T) {
	s := Service{Label: "com.ember.x", BrewPATH: true}
	out := string(s.Plist("/Users/a&b/ember-x"))
	for _, want := range []string{"<string>com.ember.x</string>", "<string>/Users/a&amp;b/ember-x</string>",
		"<string>run</string>", "<key>KeepAlive</key>", "/opt/homebrew/bin"} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q:\n%s", want, out)
		}
	}
	s.BrewPATH = false
	if out := string(s.Plist("/x")); strings.Contains(out, "EnvironmentVariables") || !strings.HasSuffix(out, "</dict>\n</plist>\n") {
		t.Errorf("plist without BrewPATH:\n%s", out)
	}
}

func TestServiceUserUnit(t *testing.T) {
	u := Service{Unit: "ember-x", Description: "X"}.UserUnit("/bin/ember-x")
	if u.Name != "ember-x" || strings.Join(u.ExecStart, " ") != "/bin/ember-x run" {
		t.Fatalf("unit = %+v", u)
	}
}

type fakeSvcLaunchctl struct {
	print []byte
	err   error
	calls []string
}

func (f *fakeSvcLaunchctl) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "print":
		return f.print, f.err
	case "bootstrap":
		return []byte("boom"), f.err
	}
	return nil, nil
}

func TestServiceReload(t *testing.T) {
	s := Service{Label: "com.ember.x"}
	home := t.TempDir()
	lc := &fakeSvcLaunchctl{print: []byte("path = " + s.PlistPath(home) + "\n")}
	if err := s.Reload(lc.run, 501, s.PlistPath(home)); err != nil {
		t.Fatal(err)
	}
	want := []string{"print gui/501/com.ember.x", "bootout gui/501/com.ember.x", "bootstrap gui/501 " + s.PlistPath(home)}
	if strings.Join(lc.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", lc.calls, want)
	}
	lc = &fakeSvcLaunchctl{print: []byte("managed_by = com.apple.xpc.ServiceManagement\n")}
	_ = s.Reload(lc.run, 501, s.PlistPath(home))
	for _, c := range lc.calls {
		if strings.HasPrefix(c, "bootout") {
			t.Fatal("booted out Ember.app's job")
		}
	}
	lc = &fakeSvcLaunchctl{err: errors.New("exit 5")}
	if err := s.Reload(lc.run, 501, "/p"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("bootstrap error = %v", err)
	}
}

func TestServiceUninstallLaunchAgent(t *testing.T) {
	s := Service{Label: "com.ember.x"}
	for _, tc := range []struct {
		name, print string
		bootout     bool
		warn        string
	}{
		{"cli", "path = %s\n", true, ""},
		{"app", "managed_by = com.apple.xpc.ServiceManagement\n", false, "Ember.app's"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			p := s.PlistPath(home)
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("<plist/>"), 0o644); err != nil {
				t.Fatal(err)
			}
			print := tc.print
			if strings.Contains(print, "%s") {
				print = strings.ReplaceAll(print, "%s", p)
			}
			lc := &fakeSvcLaunchctl{print: []byte(print)}
			var warn bytes.Buffer
			if err := s.UninstallLaunchAgent(lc.run, home, 501, &warn); err != nil {
				t.Fatal(err)
			}
			booted := strings.Contains(strings.Join(lc.calls, "|"), "bootout")
			if booted != tc.bootout || !strings.Contains(warn.String(), tc.warn) {
				t.Fatalf("bootout=%v warn=%q", booted, warn.String())
			}
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatal("plist not removed")
			}
		})
	}
}

func TestServiceStatusLaunchAgent(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("systemd status is covered by systemd_test.go")
	}
	s := Service{Label: "com.ember.x"}
	home := t.TempDir()
	if got := s.Status(home); len(got) != 1 || got[0] != "LaunchAgent: NOT installed" {
		t.Fatalf("status = %q", got)
	}
	_ = os.MkdirAll(filepath.Dir(s.PlistPath(home)), 0o700)
	_ = os.WriteFile(s.PlistPath(home), nil, 0o644)
	if got := s.Status(home); got[0] != "LaunchAgent: installed at "+s.PlistPath(home) {
		t.Fatalf("status = %q", got)
	}
}

func TestConfigureSeedsDirsAndEnv(t *testing.T) {
	defer SetHostNameForTest("Dmitrys-Mac-mini")()
	home := t.TempDir()
	if err := Configure(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "ember", "sessions")); err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	if _, err := os.Stat(LogDir(home)); err != nil {
		t.Fatalf("log dir: %v", err)
	}
	vals, err := ReadEnvFile(EnvFilePath(home))
	if err != nil || vals["EMBER_SOURCE"] != "mini" {
		t.Fatalf("producer.env = %v, %v", vals, err)
	}
	if !strings.Contains(EnvFileLine(home), "mode=0600") {
		t.Fatalf("env line = %q", EnvFileLine(home))
	}
	if err := os.WriteFile(EnvFilePath(home), []byte("EMBER_SOURCE=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Configure(home); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(EnvFilePath(home)); string(b) != "EMBER_SOURCE=keep\n" {
		t.Fatalf("producer.env overwritten: %q", b)
	}
	if got := EnvFileLine(t.TempDir()); !strings.HasPrefix(got, "producer.env: MISSING at ") {
		t.Fatalf("missing env line = %q", got)
	}
}

func TestConfigureRewritesPlaceholderSource(t *testing.T) {
	defer SetHostNameForTest("Dmitrys-Mac-mini")()
	home := t.TempDir()
	envPath := EnvFilePath(home)
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("EMBER_SOURCE=\nEMBER_TOKEN=t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Configure(home); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(envPath); string(b) != "EMBER_SOURCE=mini\nEMBER_TOKEN=t\n" {
		t.Errorf("env = %q", b)
	}
}

func TestShellSafePath(t *testing.T) {
	for p, ok := range map[string]bool{
		"/usr/local/bin/ember-claude-producer": true, "/path with space/bin": false,
		"/a$b": false, "/a`b": false, "/a;b": false, "/a#b": false, "/a\nb": false,
	} {
		if ShellSafePath(p) != ok {
			t.Errorf("ShellSafePath(%q) != %v", p, ok)
		}
	}
}

func TestServerLinesAddsTokenWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	lines := ServerLines(context.Background(), t.TempDir(), Common{ServerConfigured: srv.URL})
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "server: ") {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.HasPrefix(lines[len(lines)-1], "WARNING: ") {
		t.Fatalf("no token warning for an empty token: %q", lines)
	}
}

func TestPrintSetupHintsForConfigure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var buf bytes.Buffer
	PrintSetupHintsFor(&buf, Common{Source: "mbp", ServerConfigured: "http://h:1"}, []string{HeadlessFlag}, false)
	if !strings.Contains(buf.String(), "Headless mode") {
		t.Fatalf("hints = %q", buf.String())
	}
}

func TestDoctorPreludeDefaultsSource(t *testing.T) {
	defer SetHostNameForTest("Dmitrys-Mac-mini")()
	home := t.TempDir()
	DoctorPrelude("")
	if err := os.MkdirAll(filepath.Dir(EnvFilePath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(EnvFilePath(home), []byte("EMBER_SOURCE=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	DoctorPrelude(home)
	if b, _ := os.ReadFile(EnvFilePath(home)); string(b) != "EMBER_SOURCE=mini\n" {
		t.Fatalf("env = %q", b)
	}
}
