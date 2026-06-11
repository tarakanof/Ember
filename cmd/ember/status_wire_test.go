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

	// Absent on the wire stays nil (= enabled) after a JSON round-trip.
	var fromOld StatusRequest
	if err := json.Unmarshal([]byte(`{"source":"mbp","tool":"claude","session":"s1","state":"running"}`), &fromOld); err != nil {
		t.Fatal(err)
	}
	if got := fromOld.normalized(); got.SourceCard != nil || got.SessionBar != nil {
		t.Fatal("absent wire fields must stay nil")
	}

	// Old producers may still post tokens_today — must parse, not 400.
	var legacy StatusRequest
	if err := json.Unmarshal([]byte(`{"source":"mbp","tool":"claude","session":"s1","state":"running","tokens_today":42}`), &legacy); err != nil {
		t.Fatalf("legacy tokens_today must still parse: %v", err)
	}
}
