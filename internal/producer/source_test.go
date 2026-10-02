package producer

import (
	"errors"
	"strings"
	"testing"
)

func TestIsPlaceholderSource(t *testing.T) {
	for v, want := range map[string]bool{
		"":                           true,
		"  ":                         true,
		"set-me-to-this-laptop-id":   true,
		" Set-Me-To-This-Laptop-Id ": true,
		"m4":                         false,
	} {
		if got := IsPlaceholderSource(v); got != want {
			t.Errorf("IsPlaceholderSource(%q)=%v want %v", v, got, want)
		}
	}
}

func TestNormalizeHostName(t *testing.T) {
	long := strings.Repeat("a", 40)
	for in, want := range map[string]string{
		"  Dmitrys-M4\n": "dmitrys-m4",
		"MacBook Pro":    "macbook-pro",
		"m4.local":       "m4",
		long:             strings.Repeat("a", 24),
		"":               "",
	} {
		if got := normalizeHostName(in); got != want {
			t.Errorf("normalizeHostName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestResolveSource(t *testing.T) {
	orig := hostNameFuncs
	defer func() { hostNameFuncs = orig }()
	hostNameFuncs = []func() (string, error){
		func() (string, error) { return "", errors.New("no scutil") },
		func() (string, error) { return "M4.local", nil },
	}
	if got := ResolveSource("m5"); got != "m5" {
		t.Errorf("explicit kept: got %q", got)
	}
	if got := ResolveSource("set-me-to-this-laptop-id"); got != "m4" {
		t.Errorf("placeholder: got %q", got)
	}
	if got := ResolveSource(""); got != "m4" {
		t.Errorf("empty: got %q", got)
	}
	hostNameFuncs = []func() (string, error){func() (string, error) { return "", errors.New("x") }}
	if got := ResolveSource(""); got != "" {
		t.Errorf("no host: got %q", got)
	}
}

func TestEnvExample_NoPlaceholderSource(t *testing.T) {
	if strings.Contains(EnvExample(), "set-me-to-this-laptop-id") {
		t.Error("template should carry the host default, not the placeholder")
	}
}
