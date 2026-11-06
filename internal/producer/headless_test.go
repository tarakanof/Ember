package producer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmberAppInstalled(t *testing.T) {
	home, sys := t.TempDir(), t.TempDir()
	if emberAppInstalledFor("darwin", home, sys) {
		t.Fatal("no app yet")
	}
	if err := os.MkdirAll(filepath.Join(home, "Applications", "Ember.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !emberAppInstalledFor("darwin", home, sys) {
		t.Fatal("~/Applications/Ember.app not detected")
	}
	if emberAppInstalledFor("linux", home, sys) {
		t.Fatal("linux never has Ember.app")
	}
}

func TestHeadlessFlagForcesHeadless(t *testing.T) {
	if !Headless([]string{"--headless"}, t.TempDir()) {
		t.Fatal("--headless ignored")
	}
}
