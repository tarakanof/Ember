package producer

import (
	"os"
	"path/filepath"

	"errors"
	"github.com/tarakanof/ember/internal/render"
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
		"  Dmitrys-M4\n":        "m4",
		"MacBook Pro":           "mbp",
		"m4.local":              "m4",
		"dmitrys-macbook-pro":   "mbp",
		"Dmitrys-MacBook-Air":   "mba",
		"dmitrys-mac-mini":      "mini",
		"Dmitrys-iMac":          "imac",
		"dmitrys-macbook-pro-2": "mbp-2",
		"dmitrys-laptop":        "laptop",
		"build-box":             "build-box",
		long:                    strings.Repeat("a", 24),
		"":                      "",
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

func TestEnvExample_SeedsHostDefault(t *testing.T) {
	orig := hostNameFuncs
	defer func() { hostNameFuncs = orig }()
	hostNameFuncs = []func() (string, error){func() (string, error) { return "Dmitrys-Mac-mini", nil }}
	if !strings.Contains(EnvExample(), "EMBER_SOURCE=mini\n") {
		t.Error("template should seed EMBER_SOURCE=mini")
	}
	if strings.Contains(EnvExample(), "set-me-to-this-laptop-id") {
		t.Error("template should carry the host default, not the placeholder")
	}
}

func TestDefaultNamesDistinctOnClockCard(t *testing.T) {
	seen := map[string]string{}
	for _, h := range []string{"Dmitrys-MacBook-Pro", "Dmitrys-MacBook-Air", "Dmitrys-Mac-mini"} {
		id := normalizeHostName(h)
		card := render.SourceCardText(id)
		if card != strings.ToUpper(id) {
			t.Errorf("%q truncated on card: %q", id, card)
		}
		if prev, ok := seen[card]; ok {
			t.Errorf("%q and %q collide as %q", h, prev, card)
		}
		seen[card] = h
	}
}

func envPathIn(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "producer.env")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnsureSourceInEnv(t *testing.T) {
	orig := hostNameFuncs
	defer func() { hostNameFuncs = orig }()
	hostNameFuncs = []func() (string, error){func() (string, error) { return "Dmitrys-MacBook-Pro", nil }}

	p := envPathIn(t, "# c\nEMBER_SOURCE=set-me-to-this-laptop-id\nEMBER_TOKEN=t\n", 0o600)
	got, changed, err := EnsureSourceInEnv(p)
	if err != nil || !changed || got != "mbp" {
		t.Fatalf("got %q %v %v", got, changed, err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "# c\nEMBER_SOURCE=mbp\nEMBER_TOKEN=t\n" {
		t.Errorf("body: %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("perm %v", st.Mode().Perm())
	}

	// explicit untouched
	p = envPathIn(t, "EMBER_SOURCE=m5\n", 0o600)
	got, changed, _ = EnsureSourceInEnv(p)
	if changed || got != "m5" {
		t.Errorf("explicit: %q %v", got, changed)
	}

	// missing key appended
	p = envPathIn(t, "EMBER_TOKEN=t\n", 0o600)
	got, changed, _ = EnsureSourceInEnv(p)
	b, _ = os.ReadFile(p)
	if !changed || got != "mbp" || string(b) != "EMBER_TOKEN=t\nEMBER_SOURCE=mbp\n" {
		t.Errorf("append: %q %v %q", got, changed, b)
	}

	// bad perms: error, untouched
	p = envPathIn(t, "EMBER_SOURCE=\n", 0o644)
	if _, changed, err = EnsureSourceInEnv(p); err == nil || changed {
		t.Errorf("bad perms should error, changed=%v", changed)
	}

	// missing file: no-op
	if _, changed, err = EnsureSourceInEnv(filepath.Join(t.TempDir(), "nope")); err != nil || changed {
		t.Errorf("missing: %v %v", changed, err)
	}
}
