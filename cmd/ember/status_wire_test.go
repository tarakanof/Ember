package main

import (
	"encoding/json"
	"testing"
)

func TestStatusCarriesSourceCardAndSessionBar(t *testing.T) {
	f := false
	req := StatusRequest{Source: "mbp", Tool: "claude", Session: "s1", State: "running",
		SourceCard: &f, SessionBar: &f}
	s := req.normalized()
	if s.SourceCard == nil || *s.SourceCard {
		t.Fatal("source_card=false not preserved")
	}
	if s.SessionBar == nil || *s.SessionBar {
		t.Fatal("session_bar=false not preserved")
	}

	var fromOld StatusRequest
	if err := json.Unmarshal([]byte(`{"source":"mbp","tool":"claude","session":"s1","state":"running"}`), &fromOld); err != nil {
		t.Fatal(err)
	}
	if got := fromOld.normalized(); got.SourceCard != nil || got.SessionBar != nil {
		t.Fatal("absent wire fields must stay nil")
	}

	var legacy StatusRequest
	if err := json.Unmarshal([]byte(`{"source":"mbp","tool":"claude","session":"s1","state":"running","tokens_today":42}`), &legacy); err != nil {
		t.Fatalf("legacy tokens_today must still parse: %v", err)
	}
}

func TestStatusCarriesTheProjectTrimmedAndCut(t *testing.T) {
	req := StatusRequest{Source: "m4", Tool: "claude", Session: "s", State: "waiting",
		Project: "  learn-language-with-a-very-long-folder-name  "}
	if got := req.normalized().Project; got != "learn-language-with-a-very-long-" {
		t.Fatalf("project = %q, want the first 32 runes", got)
	}
	if got := (StatusRequest{Source: "m4", Tool: "claude", Session: "s", State: "idle"}).normalized().Project; got != "" {
		t.Fatalf("an old producer's project = %q, want empty", got)
	}
}
