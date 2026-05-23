package main

import (
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

func TestIsEnvOn(t *testing.T) {
	for _, v := range []string{"true", "1", "yes", "on", "ON"} {
		if !isEnvOn(v) {
			t.Errorf("isEnvOn(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "false", "0", "off", "no", "garbage"} {
		if isEnvOn(v) {
			t.Errorf("isEnvOn(%q) = true, want false (default off)", v)
		}
	}
}

func TestFormFromEnv(t *testing.T) {
	rec := &envRec{}
	rec.set("STATUS_SOURCE", "mbp")
	rec.set("STATUS_SERVER_URL", "http://localhost:8080")
	rec.set("STATUS_SOURCE_COLOR", "#aa66ff")
	rec.set("STATUS_CONTEXT_PCT_ENABLED", "true")
	rec.set("STATUS_RATE_PCT_ENABLED", "false")
	rec.set("STATUS_ACTIVITY_DETAIL_ENABLED", "false")
	rec.set("STATUS_ACTIVITY_TRAIL_ENABLED", "false")
	rec.set("STATUS_CONTEXT_NUMBER_ENABLED", "true")
	rec.set("STATUS_TOKEN", "secret")

	f, tokenSet := formFromEnv(rec)
	if !tokenSet {
		t.Error("tokenSet = false, want true")
	}
	if f.Token != "" {
		t.Errorf("Token = %q, want empty (never round-tripped)", f.Token)
	}
	if f.Source != "mbp" || f.ServerURL != "http://localhost:8080" || f.SourceColor != "#aa66ff" {
		t.Errorf("text fields mismapped: %+v", f)
	}
	if !f.ContextPct || f.RatePct {
		t.Errorf("ContextPct=%v RatePct=%v, want true,false", f.ContextPct, f.RatePct)
	}
	if f.ActivityDetail {
		t.Errorf("ActivityDetail = true, want false")
	}
	if f.ActivityTrail {
		t.Errorf("ActivityTrail = true, want false")
	}
	if !f.ContextNumber {
		t.Error("ContextNumber = false, want true")
	}

	empty := &envRec{}
	f2, tokenSet2 := formFromEnv(empty)
	if tokenSet2 {
		t.Error("tokenSet on empty = true, want false")
	}
	if !f2.ContextPct || !f2.RatePct || !f2.ActivityDetail || !f2.ActivityTrail {
		t.Error("absent toggles should default true (isEnvTrue semantics)")
	}
	if f2.ContextNumber {
		t.Error("ContextNumber should default false when absent")
	}
}

func TestValidateForm(t *testing.T) {
	good := settingsForm{Source: "mbp", ServerURL: "http://h:8080", SourceColor: "#aa66ff"}
	if errs := validateForm(good); len(errs) != 0 {
		t.Errorf("valid form produced errors: %v", errs)
	}
	if errs := validateForm(settingsForm{Source: "mbp", ServerURL: "http://h", SourceColor: "", Token: ""}); len(errs) != 0 {
		t.Errorf("blanks should be valid: %v", errs)
	}
	bad := settingsForm{Source: "", ServerURL: "ftp://x", SourceColor: "nope", Token: "ab\nc"}
	errs := validateForm(bad)
	for _, k := range []string{"STATUS_SOURCE", "STATUS_SERVER_URL", "STATUS_SOURCE_COLOR", "STATUS_TOKEN"} {
		if _, ok := errs[k]; !ok {
			t.Errorf("expected error for %s, got none (errs=%v)", k, errs)
		}
	}
}

func TestApplyForm(t *testing.T) {
	rec := &envRec{}
	rec.set("STATUS_TOKEN", "old-token")
	rec.set("STATUS_UNKNOWN", "keepme")

	applyForm(rec, settingsForm{Source: " mbp ", ServerURL: "http://h:8080", SourceColor: "#aa66ff", ContextPct: true, RatePct: false, ActivityDetail: true, ActivityTrail: true, ContextNumber: true, Token: ""})
	if rec.get("STATUS_TOKEN") != "old-token" {
		t.Errorf("blank token should keep existing, got %q", rec.get("STATUS_TOKEN"))
	}
	if rec.get("STATUS_SOURCE") != "mbp" {
		t.Errorf("source not normalized/trimmed, got %q", rec.get("STATUS_SOURCE"))
	}
	if rec.get("STATUS_CONTEXT_PCT_ENABLED") != "true" || rec.get("STATUS_RATE_PCT_ENABLED") != "false" {
		t.Errorf("checkbox serialization wrong: ctx=%q rate=%q", rec.get("STATUS_CONTEXT_PCT_ENABLED"), rec.get("STATUS_RATE_PCT_ENABLED"))
	}
	if rec.get("STATUS_ACTIVITY_DETAIL_ENABLED") != "true" {
		t.Errorf("activity-detail serialization wrong: %q", rec.get("STATUS_ACTIVITY_DETAIL_ENABLED"))
	}
	if rec.get("STATUS_ACTIVITY_TRAIL_ENABLED") != "true" {
		t.Errorf("activity-trail serialization wrong: %q", rec.get("STATUS_ACTIVITY_TRAIL_ENABLED"))
	}
	if rec.get("STATUS_CONTEXT_NUMBER_ENABLED") != "true" {
		t.Errorf("context-number serialization wrong: %q", rec.get("STATUS_CONTEXT_NUMBER_ENABLED"))
	}
	if rec.get("STATUS_UNKNOWN") != "keepme" {
		t.Error("unknown key not preserved")
	}

	applyForm(rec, settingsForm{Source: "mbp", ServerURL: "http://h:8080", Token: "new-token"})
	if rec.get("STATUS_TOKEN") != "new-token" {
		t.Errorf("non-blank token should replace, got %q", rec.get("STATUS_TOKEN"))
	}
}
