package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const repoRoot = "../.."

var pluginDir = filepath.Join(repoRoot, "producers", "claude-code", "plugin")

type pluginHandler struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Async   bool     `json:"async"`
	Timeout float64  `json:"timeout"`
}

type pluginGroup struct {
	Matcher string          `json:"matcher"`
	Hooks   []pluginHandler `json:"hooks"`
}

func readPluginHooks(t *testing.T) map[string][]pluginGroup {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(pluginDir, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Hooks map[string][]pluginGroup `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	return file.Hooks
}

// The plugin and the settings.json installer must register the same hook set,
// or a Mac's behavior would depend on how the producer was installed.
func TestPluginHooksMatchInstaller(t *testing.T) {
	hooks := readPluginHooks(t)
	seen := map[string]bool{}
	for _, spec := range producerHookSpecs {
		seen[spec.event] = true
		groups := hooks[spec.event]
		if len(groups) != 1 {
			t.Errorf("%s: plugin has %d matcher groups, want 1", spec.event, len(groups))
			continue
		}
		g := groups[0]
		if g.Matcher != spec.matcher {
			t.Errorf("%s: plugin matcher %q, installer %q", spec.event, g.Matcher, spec.matcher)
		}
		if len(g.Hooks) != 1 {
			t.Errorf("%s: plugin has %d handlers, want 1", spec.event, len(g.Hooks))
			continue
		}
		h := g.Hooks[0]
		wantArgs := []string{"${CLAUDE_PLUGIN_ROOT}/scripts/ember-hook", spec.subcommand}
		if h.Type != "command" || h.Command != "/bin/sh" || strings.Join(h.Args, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Errorf("%s: handler = %+v, want /bin/sh %v", spec.event, h, wantArgs)
		}
		if h.Async != spec.async {
			t.Errorf("%s: plugin async=%v, installer async=%v", spec.event, h.Async, spec.async)
		}
		if h.Timeout != float64(spec.timeout) {
			t.Errorf("%s: plugin timeout %vs, installer %ds", spec.event, h.Timeout, spec.timeout)
		}
		if !h.Async && (h.Timeout <= 0 || h.Timeout > 5) {
			t.Errorf("%s: blocking hook timeout %vs, want 0 < t <= 5 so a hung hook can't stall a session", spec.event, h.Timeout)
		}
	}
	for ev := range hooks {
		if !seen[ev] {
			t.Errorf("plugin registers %s, which the installer doesn't", ev)
		}
	}
}

func TestMarketplaceListsPlugin(t *testing.T) {
	var mp struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &mp); err != nil {
		t.Fatal(err)
	}
	if len(mp.Plugins) != 1 {
		t.Fatalf("marketplace lists %d plugins, want 1", len(mp.Plugins))
	}
	p := mp.Plugins[0]
	if got := p.Name + "@" + mp.Name; got != pluginID {
		t.Errorf("install id %q, want %q (doctor/configure look for it in enabledPlugins)", got, pluginID)
	}
	if filepath.Clean(filepath.Join(repoRoot, p.Source)) != filepath.Clean(pluginDir) {
		t.Errorf("marketplace source %q doesn't point at %s", p.Source, pluginDir)
	}
	var manifest struct {
		Name string `json:"name"`
	}
	raw, err = os.ReadFile(filepath.Join(pluginDir, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != p.Name {
		t.Errorf("plugin.json name %q != marketplace entry name %q", manifest.Name, p.Name)
	}
}

type hookRun struct {
	stdout, stderr string
	err            error
}

func runEmberHook(t *testing.T, env []string, stdin, event string) hookRun {
	t.Helper()
	script, err := filepath.Abs(filepath.Join(pluginDir, "scripts", "ember-hook"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script, event)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return hookRun{out.String(), errb.String(), err}
}

func writeFakeProducer(t *testing.T, path string, exit int) string {
	t.Helper()
	rec := path + ".rec"
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" > '" + rec + "'\ncat >> '" + rec + "'\necho noisy-stdout\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return rec
}

func hermeticEnv(home string, extra ...string) []string {
	return append([]string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin",
		"EMBER_APP_DIRS=" + filepath.Join(home, "Apps"),
	}, extra...)
}

func TestEmberHook_NoBinaryExitsZeroSilently(t *testing.T) {
	home := t.TempDir()
	r := runEmberHook(t, hermeticEnv(home), `{"session_id":"s"}`, "pre-tool-use")
	if r.err != nil || r.stdout != "" || r.stderr != "" {
		t.Fatalf("missing binary: err=%v stdout=%q stderr=%q, want silent exit 0", r.err, r.stdout, r.stderr)
	}
}

func TestEmberHook_ExplicitMissingOverrideExitsZero(t *testing.T) {
	home := t.TempDir()
	writeFakeProducer(t, filepath.Join(home, "go", "bin", "ember-claude-producer"), 0)
	r := runEmberHook(t, hermeticEnv(home, "EMBER_CLAUDE_PRODUCER="+filepath.Join(home, "nope")), `{}`, "stop")
	if r.err != nil || r.stdout != "" {
		t.Fatalf("err=%v stdout=%q", r.err, r.stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "go", "bin", "ember-claude-producer.rec")); !os.IsNotExist(err) {
		t.Fatalf("an explicit EMBER_CLAUDE_PRODUCER must not fall back to another binary")
	}
}

func TestEmberHook_ForwardsEventAndStdin_NeverFails(t *testing.T) {
	cases := []struct {
		name string
		bin  func(home string) string
		env  func(home, bin string) []string
	}{
		{"override", func(h string) string { return filepath.Join(h, "custom", "ecp") },
			func(h, b string) []string { return hermeticEnv(h, "EMBER_CLAUDE_PRODUCER="+b) }},
		{"PATH", func(h string) string { return filepath.Join(h, "pathbin", "ember-claude-producer") },
			func(h, b string) []string {
				env := hermeticEnv(h)
				env[1] = "PATH=" + filepath.Dir(b) + ":/usr/bin:/bin"
				return env
			}},
		{"go bin", func(h string) string { return filepath.Join(h, "go", "bin", "ember-claude-producer") },
			func(h, b string) []string { return hermeticEnv(h) }},
		{"local bin", func(h string) string { return filepath.Join(h, ".local", "bin", "ember-claude-producer") },
			func(h, b string) []string { return hermeticEnv(h) }},
		{"app bundle", func(h string) string {
			return filepath.Join(h, "Apps", "Ember.app", "Contents", "MacOS", "ember-claude-producer")
		}, func(h, b string) []string { return hermeticEnv(h) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			bin := c.bin(home)
			rec := writeFakeProducer(t, bin, 3)
			r := runEmberHook(t, c.env(home, bin), `{"session_id":"abc"}`, "user-prompt-submit")
			if r.err != nil {
				t.Fatalf("hook must exit 0 even when the producer fails: %v", r.err)
			}
			if r.stdout != "" {
				t.Fatalf("hook stdout must stay empty (Claude reads it), got %q", r.stdout)
			}
			got, err := os.ReadFile(rec)
			if err != nil {
				t.Fatalf("producer not invoked: %v", err)
			}
			if want := "hook user-prompt-submit\n{\"session_id\":\"abc\"}"; string(got) != want {
				t.Fatalf("producer saw %q, want %q", got, want)
			}
		})
	}
}

func TestEmberHook_LogsToXDGStateDirWithoutLibraryLogs(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "go", "bin", "ember-claude-producer")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho to-the-log\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(home, ".local", "state", "ember", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := runEmberHook(t, hermeticEnv(home), `{}`, "stop"); r.err != nil || r.stdout != "" {
		t.Fatalf("err=%v stdout=%q", r.err, r.stdout)
	}
	got, _ := os.ReadFile(filepath.Join(logDir, "ember-claude-producer.log"))
	if !strings.Contains(string(got), "to-the-log") {
		t.Fatalf("producer output not logged under ~/.local/state/ember/logs: %q", got)
	}
}
