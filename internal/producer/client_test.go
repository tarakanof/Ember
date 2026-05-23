package producer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_PostSendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "secret-token", time.Second)
	if err := c.Post(context.Background(), StatusRequest{Source: "x", Tool: "codex", Session: "s", State: "running"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", gotAuth)
	}
}

func TestClient_NoAuthWhenTokenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", time.Second)
	if err := c.Post(context.Background(), StatusRequest{Source: "x", Tool: "codex", Session: "s", State: "running"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty", gotAuth)
	}
}

func TestClient_DeleteSendsBody(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", time.Second)
	if err := c.Delete(context.Background(), DeleteRequest{Source: "mbp", Tool: "codex", Session: "s"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"source":"mbp"`) {
		t.Errorf("DELETE body missing source: %q", body)
	}
}

func TestClient_NonSuccessErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	c := NewClient(srv.URL, "", time.Second)
	if err := c.Post(context.Background(), StatusRequest{Source: "x", Tool: "codex", Session: "s", State: "running"}); err == nil {
		t.Error("401 should error")
	}
}

func TestClient_EmptyURLErrors(t *testing.T) {
	c := NewClient("", "", time.Second)
	if err := c.Post(context.Background(), StatusRequest{Source: "x", Tool: "codex", Session: "s", State: "running"}); err == nil {
		t.Error("empty URL should error")
	}
}

func TestStatusRequest_OmitsNilPointers(t *testing.T) {
	b, err := json.Marshal(StatusRequest{Source: "x", Tool: "codex", Session: "s", State: "running"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"context_pct", "source_color", "rate_window_pct"} {
		if bytes.Contains(b, []byte(f)) {
			t.Errorf("JSON must omit %s when nil, got: %s", f, b)
		}
	}
}

func TestStatusRequest_SerializesRateWindowPct(t *testing.T) {
	rw := 42
	b, err := json.Marshal(StatusRequest{Source: "x", Tool: "codex", Session: "s", State: "running", RateWindowPct: &rw})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"rate_window_pct":42`)) {
		t.Errorf("missing rate_window_pct:42, got: %s", b)
	}
}

func TestStatusRequest_SerializesRateBottomBar(t *testing.T) {
	b, err := json.Marshal(StatusRequest{Source: "x", Tool: "claude", Session: "s", State: "running", RateBottomBar: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"rate_bottom_bar":true`) {
		t.Errorf("missing rate_bottom_bar in %s", b)
	}
}

func TestStatusRequest_OmitsRateBottomBarWhenFalse(t *testing.T) {
	b, err := json.Marshal(StatusRequest{Source: "x", Tool: "claude", Session: "s", State: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "rate_bottom_bar") {
		t.Errorf("rate_bottom_bar should be omitted when false, got %s", b)
	}
}

func TestStatusRequest_SerializesRateReset(t *testing.T) {
	b, err := json.Marshal(StatusRequest{Source: "x", Tool: "claude", Session: "s", State: "running", RateResetAt: 1778614633, RateReset: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"rate_reset_at":1778614633`) || !strings.Contains(string(b), `"rate_reset":true`) {
		t.Errorf("missing reset fields in %s", b)
	}
}

func TestStatusRequest_OmitsResetWhenZero(t *testing.T) {
	b, _ := json.Marshal(StatusRequest{Source: "x", Tool: "claude", Session: "s", State: "running"})
	if strings.Contains(string(b), "rate_reset") {
		t.Errorf("reset fields should be omitted when zero/false, got %s", b)
	}
}
