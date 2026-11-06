package producer

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

const SourcePlaceholder = "set-me-to-this-laptop-id"

const maxSourceLen = 24

var hostNameFuncs = hostNameFuncsFor(runtime.GOOS)

func hostNameFuncsFor(goos string) []func() (string, error) {
	if goos != "darwin" {
		return []func() (string, error){os.Hostname}
	}
	return []func() (string, error){
		func() (string, error) {
			out, err := exec.Command("scutil", "--get", "LocalHostName").Output()
			return string(out), err
		},
		os.Hostname,
	}
}

func IsPlaceholderSource(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || strings.EqualFold(v, SourcePlaceholder)
}

func DefaultSource() string {
	for _, f := range hostNameFuncs {
		if h, err := f(); err == nil {
			if n := normalizeHostName(h); n != "" {
				return n
			}
		}
	}
	return ""
}

func ResolveSource(v string) string {
	if !IsPlaceholderSource(v) {
		return strings.TrimSpace(v)
	}
	return DefaultSource()
}

func normalizeHostName(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.IndexByte(h, '.'); i >= 0 {
		h = h[:i]
	}
	h = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r == ' ':
			return '-'
		}
		return -1
	}, h)
	h = strings.Trim(h, "-_")
	h = shortHostID(h)
	if len(h) > maxSourceLen {
		h = strings.Trim(h[:maxSourceLen], "-_")
	}
	return h
}

var modelAbbrev = map[string]string{
	"macbook-pro": "mbp",
	"macbook-air": "mba",
	"macbook":     "mb",
	"mbp":         "mbp",
	"mba":         "mba",
	"mac-mini":    "mini",
	"mac-studio":  "studio",
	"mac-pro":     "macpro",
	"imac":        "imac",
}

var (
	modelRe = regexp.MustCompile(`(?:^|-)(macbook-pro|macbook-air|macbook|mac-mini|mac-studio|mac-pro|imac|mbp|mba)(?:-(.+))?$`)
)

func shortHostID(h string) string {
	if m := modelRe.FindStringSubmatch(h); m != nil {
		id := modelAbbrev[m[1]]
		if m[2] != "" {
			id += "-" + m[2]
		}
		if id != "" && !allDigits(id) {
			return id
		}
	}
	return h
}

func EnsureSourceInEnv(path string) (string, bool, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return "", false, nil
	}
	vals, err := ReadEnvFile(path)
	if err != nil {
		return "", false, err
	}
	cur := vals["EMBER_SOURCE"]
	if !IsPlaceholderSource(cur) {
		return strings.TrimSpace(cur), false, nil
	}
	def := DefaultSource()
	if def == "" {
		return "", false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(string(raw), "\n")
	found := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if k, _, ok := strings.Cut(t, "="); ok && strings.TrimSpace(k) == "EMBER_SOURCE" {
			lines[i] = "EMBER_SOURCE=" + def
			found = true
		}
	}
	out := strings.Join(lines, "\n")
	if !found {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "EMBER_SOURCE=" + def + "\n"
	}
	if err := WriteFileAtomic(path, []byte(out), 0o600); err != nil {
		return "", false, err
	}
	return def, true, nil
}

func SourceHint(src string) string {
	return fmt.Sprintf("EMBER_SOURCE = %q (shown on the clock card, ~4 glyphs); set EMBER_SOURCE in ~/.config/ember/producer.env to choose a different short id", src)
}

func SetHostNameForTest(name string) (restore func()) {
	orig := hostNameFuncs
	hostNameFuncs = []func() (string, error){func() (string, error) { return name, nil }}
	return func() { hostNameFuncs = orig }
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
