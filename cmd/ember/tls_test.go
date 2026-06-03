package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// genSelfSignedPEM writes a fresh self-signed P-256 cert + key as PEM
// files into dir and returns (certPath, keyPath). The cert is valid for
// 1 hour, has subjectAltName=IP:127.0.0.1, and is marked CA-capable so
// it can be added directly to a trust pool by the healthcheck client
// tests. Self-signed + IsCA=true means the same cert acts as both leaf
// and trust anchor — fine for our test scope.
func genSelfSignedPEM(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "awtrix-test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certPEM, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("os.Create cert: %v", err)
	}
	defer certPEM.Close()
	if err := pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("pem.Encode cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	keyPEM, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("os.Create key: %v", err)
	}
	defer keyPEM.Close()
	if err := pem.Encode(keyPEM, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatalf("pem.Encode key: %v", err)
	}
	return certPath, keyPath
}

func TestReadTLSEnv_NeitherSet(t *testing.T) {
	t.Setenv(envTLSCertFile, "")
	t.Setenv(envTLSKeyFile, "")
	got, err := readTLSEnv()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.enabled {
		t.Errorf("enabled = true, want false")
	}
}

func TestReadTLSEnv_OnlyCertSet(t *testing.T) {
	t.Setenv(envTLSCertFile, "/some/cert.pem")
	t.Setenv(envTLSKeyFile, "")
	_, err := readTLSEnv()
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), envTLSCertFile) || !strings.Contains(err.Error(), envTLSKeyFile) {
		t.Errorf("err %q should mention both env names", err)
	}
}

func TestReadTLSEnv_OnlyKeySet(t *testing.T) {
	t.Setenv(envTLSCertFile, "")
	t.Setenv(envTLSKeyFile, "/some/key.pem")
	_, err := readTLSEnv()
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), envTLSCertFile) || !strings.Contains(err.Error(), envTLSKeyFile) {
		t.Errorf("err %q should mention both env names", err)
	}
}

func TestReadTLSEnv_BothSet_Valid(t *testing.T) {
	dir := t.TempDir()
	cert, key := genSelfSignedPEM(t, dir)
	t.Setenv(envTLSCertFile, cert)
	t.Setenv(envTLSKeyFile, key)
	got, err := readTLSEnv()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !got.enabled {
		t.Fatal("enabled = false, want true")
	}
	if len(got.cert.Certificate) == 0 {
		t.Error("cert.Certificate is empty, want parsed cert chain")
	}
}

func TestReadTLSEnv_BothSet_BadCert(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envTLSCertFile, bad)
	t.Setenv(envTLSKeyFile, bad)
	_, err := readTLSEnv()
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("err %q should mention the file path %q", err, bad)
	}
}

func TestServeTLS_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genSelfSignedPEM(t, dir)
	tlsKP, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	tlsListener := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{tlsKP},
		MinVersion:   tls.VersionTLS12,
	})

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	server := &http.Server{Handler: app.routes()}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(tlsListener) }()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test only
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("https://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// runServerWithEnv invokes `go run .` with the given extra env. Returns
// the combined stdout+stderr output + the exit error. Used to assert main()'s
// slog.Error + os.Exit(1) on TLS misconfig BEFORE binding the listener —
// so no port collision. slog writes to os.Stdout, so we capture both.
func runServerWithEnv(t *testing.T, extraEnv ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Env = append(cmd.Environ(), extraEnv...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func TestMain_PartialTLSEnvExits1(t *testing.T) {
	stderr, err := runServerWithEnv(t,
		"EMBER_TLS_CERT_FILE=/nonexistent/cert.pem",
		"EMBER_TLS_KEY_FILE=",
	)
	if err == nil {
		t.Fatal("expected non-zero exit, got nil")
	}
	if !strings.Contains(stderr, "TLS misconfigured") {
		t.Errorf("stderr should mention TLS misconfig; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "EMBER_TLS_CERT_FILE") || !strings.Contains(stderr, "EMBER_TLS_KEY_FILE") {
		t.Errorf("stderr should name both env vars; got:\n%s", stderr)
	}
}

func TestMain_BadTLSCertExits1(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, err := runServerWithEnv(t,
		"EMBER_TLS_CERT_FILE="+bad,
		"EMBER_TLS_KEY_FILE="+bad,
	)
	if err == nil {
		t.Fatal("expected non-zero exit, got nil")
	}
	if !strings.Contains(stderr, "TLS load") {
		t.Errorf("stderr should mention TLS load failure; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, bad) {
		t.Errorf("stderr should name the bad cert path %q; got:\n%s", bad, stderr)
	}
}
