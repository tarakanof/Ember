package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func deadAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("listener.Close: %v", err)
	}
	return "http://" + addr + "/healthz"
}

func TestHealthcheckTarget_Default(t *testing.T) {
	t.Setenv("EMBER_HEALTHCHECK_URL", "")
	got := healthcheckTarget()
	want := "http://127.0.0.1:3627/healthz"
	if got != want {
		t.Errorf("healthcheckTarget() = %q, want %q", got, want)
	}
}

func TestHealthcheckTarget_Override(t *testing.T) {
	t.Setenv("EMBER_HEALTHCHECK_URL", "http://example.test/x")
	got := healthcheckTarget()
	want := "http://example.test/x"
	if got != want {
		t.Errorf("healthcheckTarget() = %q, want %q", got, want)
	}
}

func TestHealthcheckOnce_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := healthcheckOnce(srv.URL+"/healthz", guardedTransport); err != nil {
		t.Fatalf("healthcheckOnce: unexpected error: %v", err)
	}
}

func TestHealthcheckOnce_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := healthcheckOnce(srv.URL+"/healthz", guardedTransport)
	if err == nil {
		t.Fatal("healthcheckOnce: expected error on 503, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("healthcheckOnce: error %q should mention status code 503", err)
	}
}

func TestHealthcheckOnce_Down(t *testing.T) {
	err := healthcheckOnce(deadAddr(t), guardedTransport)
	if err == nil {
		t.Fatal("healthcheckOnce: expected error against closed port, got nil")
	}
}

func runHealthcheckSubprocess(t *testing.T, healthURL string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "healthcheck")
	cmd.Env = append(isolatedGoRunEnv(t),
		"EMBER_HEALTHCHECK_URL="+healthURL,
		"CONFIG_PATH=/nonexistent/awtrix.json",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("subprocess: %w (stderr: %s)", err, stderr.String())
	}
	return nil
}

func TestHealthcheck_OKAgainstUpServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := runHealthcheckSubprocess(t, srv.URL+"/healthz"); err != nil {
		t.Fatalf("healthcheck should exit 0 against healthy server: %v", err)
	}
}

func TestHealthcheck_FailWhenDown(t *testing.T) {
	if err := runHealthcheckSubprocess(t, deadAddr(t)); err == nil {
		t.Fatal("healthcheck should exit non-zero against closed port")
	}
}

func TestHealthcheck_FailOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := runHealthcheckSubprocess(t, srv.URL+"/healthz"); err == nil {
		t.Fatal("healthcheck should exit non-zero on 503")
	}
}

func TestHealthcheckTarget_TLSDefault(t *testing.T) {
	t.Setenv("EMBER_HEALTHCHECK_URL", "")
	t.Setenv("EMBER_TLS_CERT_FILE", "/some/path")
	got := healthcheckTarget()
	want := "https://127.0.0.1:3627/healthz"
	if got != want {
		t.Errorf("healthcheckTarget() = %q, want %q", got, want)
	}
}

func TestHealthcheckTarget_OverrideWinsOverTLS(t *testing.T) {
	t.Setenv("EMBER_HEALTHCHECK_URL", "http://example.test/x")
	t.Setenv("EMBER_TLS_CERT_FILE", "/some/path")
	got := healthcheckTarget()
	want := "http://example.test/x"
	if got != want {
		t.Errorf("healthcheckTarget() = %q, want %q", got, want)
	}
}

func TestHealthcheckOnce_HTTPS_TrustedViaCAFile(t *testing.T) {
	dir := t.TempDir()
	cert, key := genSelfSignedPEM(t, dir)
	tlsKP, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsKP}}
	srv.StartTLS()
	defer srv.Close()

	t.Setenv("EMBER_HEALTHCHECK_CA_FILE", cert)
	t.Setenv("EMBER_HEALTHCHECK_INSECURE", "")
	if err := healthcheckOnce(srv.URL+"/healthz", guardedTransport); err != nil {
		t.Fatalf("healthcheckOnce: %v", err)
	}
}

func TestHealthcheckOnce_HTTPS_Insecure(t *testing.T) {
	dir := t.TempDir()
	cert, key := genSelfSignedPEM(t, dir)
	tlsKP, _ := tls.LoadX509KeyPair(cert, key)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsKP}}
	srv.StartTLS()
	defer srv.Close()

	t.Setenv("EMBER_HEALTHCHECK_CA_FILE", "")
	t.Setenv("EMBER_HEALTHCHECK_INSECURE", "1")
	if err := healthcheckOnce(srv.URL+"/healthz", guardedTransport); err != nil {
		t.Fatalf("healthcheckOnce: %v", err)
	}
}

func TestHealthcheckOnce_HTTPS_UntrustedFails(t *testing.T) {
	dir := t.TempDir()
	cert, key := genSelfSignedPEM(t, dir)
	tlsKP, _ := tls.LoadX509KeyPair(cert, key)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsKP}}
	srv.StartTLS()
	defer srv.Close()

	t.Setenv("EMBER_HEALTHCHECK_CA_FILE", "")
	t.Setenv("EMBER_HEALTHCHECK_INSECURE", "")
	if err := healthcheckOnce(srv.URL+"/healthz", guardedTransport); err == nil {
		t.Fatal("healthcheckOnce: expected verify failure against unknown CA, got nil")
	}
}

func TestLoadCAPool_RejectsNonPEM(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(bad, []byte("this is not a PEM bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadCAPool(bad)
	if err == nil {
		t.Fatal("loadCAPool: expected error for non-PEM file, got nil")
	}
	if !strings.Contains(err.Error(), "no PEM certs found") {
		t.Errorf("loadCAPool error = %q; want it to mention \"no PEM certs found\"", err)
	}
}

func TestHealthcheckOnce_HTTPSIgnoresTheBaseProxyAndHTTP2(t *testing.T) {
	var proxied atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Store(true)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	var proto atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto.Store(int32(r.ProtoMajor))
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := guardedTransport.Clone()
	base.Proxy, base.ForceAttemptHTTP2 = http.ProxyURL(proxyURL), true
	t.Setenv("EMBER_HEALTHCHECK_CA_FILE", "")
	t.Setenv("EMBER_HEALTHCHECK_INSECURE", "1")
	if err := healthcheckOnce(srv.URL+"/healthz", base); err != nil {
		t.Fatalf("healthcheckOnce: %v", err)
	}
	if proxied.Load() {
		t.Error("the TLS probe went through the base transport's proxy")
	}
	if got := proto.Load(); got != 1 {
		t.Errorf("the TLS probe spoke HTTP/%d, want HTTP/1.1", got)
	}
}
