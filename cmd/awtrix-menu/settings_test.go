package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSetting(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		in      string
		want    string
		wantErr bool
	}{
		{"source ok", "STATUS_SOURCE", "  mbp ", "mbp", false},
		{"source empty", "STATUS_SOURCE", "   ", "", true},
		{"server ok", "STATUS_SERVER_URL", "http://localhost:8080", "http://localhost:8080", false},
		{"server https ok", "STATUS_SERVER_URL", "https://h:9/x", "https://h:9/x", false},
		{"server empty", "STATUS_SERVER_URL", "", "", true},
		{"server bad scheme", "STATUS_SERVER_URL", "ftp://h", "", true},
		{"server hostless", "STATUS_SERVER_URL", "http://", "", true},
		{"server with creds", "STATUS_SERVER_URL", "http://u:p@h", "", true},
		{"token ok", "STATUS_TOKEN", "abc123", "abc123", false},
		{"token control char", "STATUS_TOKEN", "ab\nc", "", true},
		{"color ok", "STATUS_SOURCE_COLOR", "#aa66ff", "#aa66ff", false},
		{"color blank ok", "STATUS_SOURCE_COLOR", "", "", false},
		{"color bad", "STATUS_SOURCE_COLOR", "aa66ff", "", true},
		{"color short", "STATUS_SOURCE_COLOR", "#abc", "", true},
		{"window ok", "STATUS_CONTEXT_WINDOW_TOKENS", "1000000", "1000000", false},
		{"window blank ok", "STATUS_CONTEXT_WINDOW_TOKENS", "", "", false},
		{"window zero ok", "STATUS_CONTEXT_WINDOW_TOKENS", "0", "0", false},
		{"window negative", "STATUS_CONTEXT_WINDOW_TOKENS", "-5", "", true},
		{"window non-numeric", "STATUS_CONTEXT_WINDOW_TOKENS", "lots", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateSetting(tc.key, tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("want error, got nil (value %q)", got)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsEnvTrue(t *testing.T) {
	for _, v := range []string{"", "true", "1", "yes", "on", "TRUE", "anything"} {
		if !isEnvTrue(v) {
			t.Errorf("isEnvTrue(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"false", "0", "no", "off", "FALSE", " off "} {
		if isEnvTrue(v) {
			t.Errorf("isEnvTrue(%q) = true, want false", v)
		}
	}
}

func TestSettingsFlip(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &settingsMenu{envPath: filepath.Join(dir, "producer.env")}

	// Absent key defaults to true (isEnvTrue), so first flip → false.
	got, err := s.flip("STATUS_RATE_PCT_ENABLED")
	if err != nil {
		t.Fatal(err)
	}
	if got != "false" {
		t.Errorf("flip from default = %q, want false", got)
	}
	// Second flip → true.
	if got, _ = s.flip("STATUS_RATE_PCT_ENABLED"); got != "true" {
		t.Errorf("second flip = %q, want true", got)
	}
	// The context key must be untouched by flipping the rate key.
	if v := s.readRec().get("STATUS_CONTEXT_PCT_ENABLED"); v != "" {
		t.Errorf("context key mutated = %q, want empty", v)
	}
}

func TestFormFromEnv(t *testing.T) {
	rec := &envRec{}
	rec.set("STATUS_SOURCE", "mbp")
	rec.set("STATUS_SERVER_URL", "http://localhost:8080")
	rec.set("STATUS_SOURCE_COLOR", "#aa66ff")
	rec.set("STATUS_CONTEXT_WINDOW_TOKENS", "1000000")
	rec.set("STATUS_CONTEXT_PCT_ENABLED", "true")
	rec.set("STATUS_RATE_PCT_ENABLED", "false")
	rec.set("STATUS_TOKEN", "secret")

	f, tokenSet := formFromEnv(rec)
	if !tokenSet {
		t.Error("tokenSet = false, want true")
	}
	if f.Token != "" {
		t.Errorf("Token = %q, want empty (never round-tripped)", f.Token)
	}
	if f.Source != "mbp" || f.ServerURL != "http://localhost:8080" || f.SourceColor != "#aa66ff" || f.ContextWindow != "1000000" {
		t.Errorf("text fields mismapped: %+v", f)
	}
	if !f.ContextPct || f.RatePct {
		t.Errorf("ContextPct=%v RatePct=%v, want true,false", f.ContextPct, f.RatePct)
	}

	empty := &envRec{}
	f2, tokenSet2 := formFromEnv(empty)
	if tokenSet2 {
		t.Error("tokenSet on empty = true, want false")
	}
	if !f2.ContextPct || !f2.RatePct {
		t.Error("absent toggles should default true (isEnvTrue semantics)")
	}
}

func TestValidateForm(t *testing.T) {
	good := settingsForm{Source: "mbp", ServerURL: "http://h:8080", SourceColor: "#aa66ff", ContextWindow: "200000"}
	if errs := validateForm(good); len(errs) != 0 {
		t.Errorf("valid form produced errors: %v", errs)
	}
	if errs := validateForm(settingsForm{Source: "mbp", ServerURL: "http://h", SourceColor: "", ContextWindow: "", Token: ""}); len(errs) != 0 {
		t.Errorf("blanks should be valid: %v", errs)
	}
	bad := settingsForm{Source: "", ServerURL: "ftp://x", SourceColor: "nope", ContextWindow: "-3", Token: "ab\nc"}
	errs := validateForm(bad)
	for _, k := range []string{"STATUS_SOURCE", "STATUS_SERVER_URL", "STATUS_SOURCE_COLOR", "STATUS_CONTEXT_WINDOW_TOKENS", "STATUS_TOKEN"} {
		if _, ok := errs[k]; !ok {
			t.Errorf("expected error for %s, got none (errs=%v)", k, errs)
		}
	}
}

func TestApplyForm(t *testing.T) {
	rec := &envRec{}
	rec.set("STATUS_TOKEN", "old-token")
	rec.set("STATUS_UNKNOWN", "keepme")

	applyForm(rec, settingsForm{Source: " mbp ", ServerURL: "http://h:8080", SourceColor: "#aa66ff", ContextWindow: "0", ContextPct: true, RatePct: false, Token: ""})
	if rec.get("STATUS_TOKEN") != "old-token" {
		t.Errorf("blank token should keep existing, got %q", rec.get("STATUS_TOKEN"))
	}
	if rec.get("STATUS_SOURCE") != "mbp" {
		t.Errorf("source not normalized/trimmed, got %q", rec.get("STATUS_SOURCE"))
	}
	if rec.get("STATUS_CONTEXT_PCT_ENABLED") != "true" || rec.get("STATUS_RATE_PCT_ENABLED") != "false" {
		t.Errorf("checkbox serialization wrong: ctx=%q rate=%q", rec.get("STATUS_CONTEXT_PCT_ENABLED"), rec.get("STATUS_RATE_PCT_ENABLED"))
	}
	if rec.get("STATUS_UNKNOWN") != "keepme" {
		t.Error("unknown key not preserved")
	}

	applyForm(rec, settingsForm{Source: "mbp", ServerURL: "http://h:8080", Token: "new-token"})
	if rec.get("STATUS_TOKEN") != "new-token" {
		t.Errorf("non-blank token should replace, got %q", rec.get("STATUS_TOKEN"))
	}
}
