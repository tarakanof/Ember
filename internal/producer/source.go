package producer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// SourcePlaceholder is the value older templates shipped for EMBER_SOURCE.
const SourcePlaceholder = "set-me-to-this-laptop-id"

const maxSourceLen = 24

// hostNameFuncs are tried in order; tests replace them.
var hostNameFuncs = []func() (string, error){
	func() (string, error) {
		out, err := exec.Command("scutil", "--get", "LocalHostName").Output()
		return string(out), err
	},
	os.Hostname,
}

// IsPlaceholderSource reports whether v is unset or the template placeholder.
func IsPlaceholderSource(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || strings.EqualFold(v, SourcePlaceholder)
}

// DefaultSource returns the short host name (macOS LocalHostName, else the OS
// hostname), lowercased and trimmed to a sane length; "" when unknown.
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

// ResolveSource keeps an explicit EMBER_SOURCE and defaults an empty or
// placeholder one to DefaultSource.
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
	"mac-mini":    "mini",
	"mac-studio":  "studio",
	"mac-pro":     "macpro",
	"imac":        "imac",
}

var (
	modelRe = regexp.MustCompile(`(?:^|-)(macbook-pro|macbook-air|macbook|mac-mini|mac-studio|mac-pro|imac|mbp|mba)(?:-(.+))?$`)
)

// shortHostID shortens a macOS default name ("dmitrys-macbook-pro") to a
// model id ("mbp") so different Macs stay distinct on the 4-glyph clock card.
// A trailing disambiguator ("-2") is kept.
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

// EnsureSourceInEnv rewrites an empty or placeholder EMBER_SOURCE in the env
// file at path to the host default (appending the key when absent), so hot
// paths read an explicit value instead of forking scutil. It returns the
// effective source and whether the file changed. A missing file is a no-op;
// the file must pass the same 0600/ownership checks as ReadEnvFile.
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".producer.env.*")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", false, err
	}
	if _, err := tmp.WriteString(out); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", false, err
	}
	return def, true, nil
}

// SourceHint is the install/doctor line naming the resolved source.
func SourceHint(src string) string {
	return fmt.Sprintf("EMBER_SOURCE = %q (shown on the clock card, ~4 glyphs); set EMBER_SOURCE in ~/.config/ember/producer.env to choose a different short id", src)
}

// SetHostNameForTest replaces the host name lookup and returns a restore func.
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
