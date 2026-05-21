package main

import "testing"

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
