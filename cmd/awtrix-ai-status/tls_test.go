package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
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
