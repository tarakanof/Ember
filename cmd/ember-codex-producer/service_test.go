package main

import (
	"strings"
	"testing"
)

func TestUserUnitRunsDaemonFromInstalledBinary(t *testing.T) {
	u := service.UserUnit("/home/u/.local/bin/ember-codex-producer")
	if u.Name != "ember-codex-producer" {
		t.Errorf("unit name %q", u.Name)
	}
	body := string(u.Render())
	for _, want := range []string{`ExecStart="/home/u/.local/bin/ember-codex-producer" "run"`, "Restart=always", "WantedBy=default.target"} {
		if !strings.Contains(body, want) {
			t.Errorf("unit missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "EMBER_TOKEN") {
		t.Error("unit must not carry token material")
	}
}
