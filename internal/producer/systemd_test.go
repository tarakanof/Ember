package producer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   map[string]string
	fail  map[string]error
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, " ")
	return []byte(f.out[key]), f.fail[key]
}

func (f *fakeRunner) joined() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

var testUnit = UserUnit{
	Name:        "ember-codex-producer",
	Description: "Ember Codex producer",
	ExecStart:   []string{"/home/u/.local/bin/ember-codex-producer", "run"},
}

func TestUserUnitRendersRestartingService(t *testing.T) {
	got := string(testUnit.Render())
	for _, want := range []string{
		"[Unit]\nDescription=Ember Codex producer\n",
		`ExecStart="/home/u/.local/bin/ember-codex-producer" "run"`,
		"Restart=always\n",
		"Nice=10\n",
		"[Install]\nWantedBy=default.target\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit missing %q:\n%s", want, got)
		}
	}
}

func TestUserUnitEscapesSpecifiersVariablesQuotesAndNewlines(t *testing.T) {
	u := UserUnit{Name: "x", Description: "50% \"x\"\nInjected=1", ExecStart: []string{"/a b/c%d\"e$HOME\nf"}}
	got := string(u.Render())
	if !strings.Contains(got, `ExecStart="/a b/c%%d\"e$$HOME\nf"`) {
		t.Errorf("ExecStart not escaped:\n%s", got)
	}
	if !strings.Contains(got, "Description=50%% \"x\" Injected=1\n") {
		t.Errorf("Description not escaped:\n%s", got)
	}
	if strings.Contains(got, "\nInjected=1") {
		t.Errorf("newline let a directive through:\n%s", got)
	}
}

func TestNewUserUnitCarriesXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/srv/u/state")
	got := string(NewUserUnit("x", "d", "/bin/x", "run").Render())
	if !strings.Contains(got, `Environment="XDG_STATE_HOME=/srv/u/state"`+"\n") {
		t.Errorf("unit missing XDG_STATE_HOME:\n%s", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	if strings.Contains(string(NewUserUnit("x", "d", "/bin/x").Render()), "Environment=") {
		t.Error("Environment= without XDG_STATE_HOME")
	}
}

func TestLingerHint(t *testing.T) {
	off := &fakeRunner{out: map[string]string{"loginctl show-user joe --property=Linger": "Linger=no\n"}}
	if h := LingerHint(off.run, "joe"); !strings.Contains(h, "sudo loginctl enable-linger joe") {
		t.Errorf("off: %q", h)
	}
	on := &fakeRunner{out: map[string]string{"loginctl show-user joe --property=Linger": "Linger=yes\n"}}
	if h := LingerHint(on.run, "joe"); h != "" {
		t.Errorf("on: %q", h)
	}
}

func TestInstallUserUnitWritesUnitAndEnablesIt(t *testing.T) {
	home := t.TempDir()
	r := &fakeRunner{}
	if err := InstallUserUnit(r.run, home, testUnit); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "systemd", "user", "ember-codex-producer.service")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unit not written: %v", err)
	}
	if string(body) != string(testUnit.Render()) {
		t.Errorf("unit body differs")
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable ember-codex-producer.service",
		"systemctl --user restart ember-codex-producer.service",
	}
	if got := r.joined(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestInstallUserUnitExplainsMissingUserBus(t *testing.T) {
	home := t.TempDir()
	r := &fakeRunner{fail: map[string]error{"systemctl --user daemon-reload": errors.New("exit status 1")},
		out: map[string]string{"systemctl --user daemon-reload": "Failed to connect to bus: No medium found"}}
	err := InstallUserUnit(r.run, home, testUnit)
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"No medium found", "loginctl enable-linger", "ember-codex-producer run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestUninstallUserUnitDisablesAndRemoves(t *testing.T) {
	home := t.TempDir()
	r := &fakeRunner{}
	if err := InstallUserUnit(r.run, home, testUnit); err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	if err := UninstallUserUnit(r.run, home, testUnit.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(UserUnitPath(home, testUnit.Name)); !os.IsNotExist(err) {
		t.Errorf("unit file left behind: %v", err)
	}
	want := []string{
		"systemctl --user disable --now ember-codex-producer.service",
		"systemctl --user daemon-reload",
	}
	if got := r.joined(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestUninstallUserUnitWithoutUnitTouchesNothing(t *testing.T) {
	r := &fakeRunner{}
	if err := UninstallUserUnit(r.run, t.TempDir(), "ember-codex-producer"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 0 {
		t.Errorf("calls = %q, want none", r.joined())
	}
}

func TestUserUnitStatusReportsActiveAndLinger(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(UserUnitPath(home, "u")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(UserUnitPath(home, "u"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{out: map[string]string{
		"systemctl --user is-active u.service":     "active\n",
		"systemctl --user is-enabled u.service":    "enabled\n",
		"loginctl show-user joe --property=Linger": "Linger=no\n",
	}}
	lines := UserUnitStatus(r.run, home, "u", "joe")
	got := strings.Join(lines, "\n")
	for _, want := range []string{"installed at", "active, enabled", "Linger=no", "loginctl enable-linger joe"} {
		if !strings.Contains(got, want) {
			t.Errorf("status missing %q:\n%s", want, got)
		}
	}
}

func TestUserUnitStatusNotInstalled(t *testing.T) {
	r := &fakeRunner{}
	got := strings.Join(UserUnitStatus(r.run, t.TempDir(), "u", "joe"), "\n")
	if !strings.Contains(got, "NOT installed") {
		t.Errorf("got %q", got)
	}
}
