package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const stalePluginFix = "claude plugin marketplace update ember && claude plugin update ember@ember, " +
	"then /reload-plugins (or a restart) in running Claude sessions"

type hookBinaryEnv struct {
	home     string
	override *string
	appDirs  []string
	pathDirs []string
}

func hookBinaryEnvFromOS(home string) hookBinaryEnv {
	e := hookBinaryEnv{home: home, pathDirs: filepath.SplitList(os.Getenv("PATH"))}
	if v, ok := os.LookupEnv("EMBER_CLAUDE_PRODUCER"); ok {
		e.override = &v
	}
	dirs := os.Getenv("EMBER_APP_DIRS")
	if dirs == "" {
		dirs = "/Applications " + filepath.Join(home, "Applications")
	}
	e.appDirs = strings.Fields(dirs)
	return e
}

func isExecFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func (e hookBinaryEnv) candidates() []string {
	var out []string
	for _, d := range e.appDirs {
		out = append(out, filepath.Join(d, "Ember.app", "Contents", "MacOS", producerName))
	}
	for _, d := range e.pathDirs {
		if d != "" {
			out = append(out, filepath.Join(d, producerName))
		}
	}
	return append(out,
		filepath.Join(e.home, "go", "bin", producerName),
		filepath.Join(e.home, ".local", "bin", producerName))
}

func (e hookBinaryEnv) resolve() (bin string, others []string) {
	if e.override != nil {
		if isExecFile(*e.override) {
			return *e.override, nil
		}
		return "", nil
	}
	seen := map[string]bool{}
	for _, c := range e.candidates() {
		if !isExecFile(c) || seen[c] {
			continue
		}
		seen[c] = true
		if bin == "" {
			bin = c
		} else {
			others = append(others, c)
		}
	}
	return bin, others
}

func sameFile(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

func binaryVersion(bin string) string {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), producerName))
}

func hookBinaryLines(e hookBinaryEnv, self string) []string {
	bin, others := e.resolve()
	if bin == "" {
		if e.override != nil {
			return []string{"hook binary: EMBER_CLAUDE_PRODUCER=" + *e.override + " is not an executable file, so the plugin hooks do nothing"}
		}
		return []string{"hook binary: none found for the plugin shim (Ember.app, PATH, ~/go/bin, ~/.local/bin), so the plugin hooks do nothing"}
	}
	lines := []string{fmt.Sprintf("hook binary: %s (%s)", bin, binaryVersion(bin))}
	if self != "" && !sameFile(bin, self) {
		lines = append(lines, fmt.Sprintf("WARNING: the hooks run %s, not this binary (%s). "+
			"Install or update Ember.app, or point EMBER_CLAUDE_PRODUCER at the build you want", bin, self))
	}
	for _, o := range others {
		if !sameFile(o, bin) {
			lines = append(lines, fmt.Sprintf("hint: %s is an older copy the hooks no longer use; delete it if you don't need it", o))
		}
	}
	return lines
}

type installedPlugins struct {
	Plugins map[string][]struct {
		InstallPath string `json:"installPath"`
	} `json:"plugins"`
}

func installedPluginPath(home string) string {
	b, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return ""
	}
	var ip installedPlugins
	if json.Unmarshal(b, &ip) != nil {
		return ""
	}
	if list := ip.Plugins[pluginID]; len(list) > 0 {
		return list[len(list)-1].InstallPath
	}
	return ""
}

func shimPrefersApp(shim string) bool {
	app := strings.Index(shim, "Ember.app/Contents/MacOS")
	path := strings.Index(shim, "command -v")
	return app >= 0 && (path < 0 || app < path)
}

func stalePluginProblems(home string) []string {
	root := installedPluginPath(home)
	if root == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		return nil
	}
	var file struct {
		Hooks map[string][]pluginHookGroup `json:"hooks"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return []string{"its hooks.json is unreadable"}
	}
	var problems []string
	for _, spec := range producerHookSpecs {
		groups := file.Hooks[spec.event]
		if len(groups) != 1 {
			problems = append(problems, spec.event+" is missing")
		} else if groups[0].Matcher != spec.matcher {
			problems = append(problems, spec.event+" matcher differs")
		}
	}
	if shim, err := os.ReadFile(filepath.Join(root, "scripts", "ember-hook")); err == nil && !shimPrefersApp(string(shim)) {
		problems = append(problems, "its ember-hook shim still prefers PATH over Ember.app")
	}
	return problems
}

type pluginHookGroup struct {
	Matcher string `json:"matcher"`
}

func stalePluginLine(home string) string {
	if !pluginEnabledAt(home) {
		return ""
	}
	problems := stalePluginProblems(home)
	if len(problems) == 0 {
		return ""
	}
	return "WARNING: the installed plugin " + pluginID + " is older than this producer (" +
		strings.Join(problems, "; ") + "). Fix: " + stalePluginFix
}
