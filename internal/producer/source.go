package producer

import (
	"os"
	"os/exec"
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
	if len(h) > maxSourceLen {
		h = strings.Trim(h[:maxSourceLen], "-_")
	}
	return h
}
