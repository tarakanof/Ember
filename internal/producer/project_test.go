package producer

import (
	"strings"
	"testing"
)

func TestProjectFromCwdTakesTheBasename(t *testing.T) {
	cases := map[string]string{
		"/Users/dt/Github/learn-language":  "learn-language",
		"/Users/dt/Github/learn-language/": "learn-language",
		"  /srv/app  ":                     "app",
		"":                                 "",
		"/":                                "",
	}
	for in, want := range cases {
		if got := ProjectFromCwd(in); got != want {
			t.Errorf("ProjectFromCwd(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProjectFromCwdCutsToThirtyTwoRunes(t *testing.T) {
	got := ProjectFromCwd("/x/" + strings.Repeat("é", 40))
	if n := len([]rune(got)); n != ProjectMaxRunes {
		t.Fatalf("len = %d runes, want %d", n, ProjectMaxRunes)
	}
}

func TestProjectFromTitleCollapsesWhitespaceAndCuts(t *testing.T) {
	if got := ProjectFromTitle("  Fix\nthe   login  "); got != "Fix the login" {
		t.Fatalf("got %q", got)
	}
	if got := ProjectFromTitle(strings.Repeat("a", 50)); len(got) != ProjectMaxRunes {
		t.Fatalf("len = %d", len(got))
	}
}

func TestSendProjectDefaultsOnAndZeroTurnsItOff(t *testing.T) {
	c := DefaultCommon()
	if c.Project("ember") != "ember" {
		t.Fatal("default must send the project")
	}
	c.Set("EMBER_SEND_PROJECT", "0")
	if c.Project("ember") != "" {
		t.Fatal("EMBER_SEND_PROJECT=0 must send none")
	}
}

func TestProjectFromCwdSkipsTheHomeDirectory(t *testing.T) {
	old := userHomeDir
	userHomeDir = func() (string, error) { return "/Users/someone", nil }
	t.Cleanup(func() { userHomeDir = old })
	if got := ProjectFromCwd("/Users/someone/"); got != "" {
		t.Fatalf("project = %q, want none for the home directory", got)
	}
	if got := ProjectFromCwd("/Users/someone/ember"); got != "ember" {
		t.Fatalf("project = %q", got)
	}
}
