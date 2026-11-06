package main

import "testing"

func TestParseClaudeCreds(t *testing.T) {
	blob := []byte(`{"claudeAiOauth":{"accessToken":"sk-abc","refreshToken":"r","expiresAt":1780000000000,"scopes":["x"],"subscriptionType":"max"}}`)
	got, err := parseClaudeCreds(blob)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.AccessToken != "sk-abc" {
		t.Errorf("token = %q", got.AccessToken)
	}
	if got.ExpiresAtMs != 1780000000000 {
		t.Errorf("expiresAt = %d", got.ExpiresAtMs)
	}
}
