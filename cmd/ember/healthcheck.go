package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultHealthcheckURL = "http://127.0.0.1:3627/healthz"

// healthcheckTarget returns the URL the in-image healthcheck should probe.
// EMBER_HEALTHCHECK_URL overrides; otherwise the default points at the
// container's loopback. When EMBER_TLS_CERT_FILE is set, the default
// scheme flips to https — keeps the in-image probe coherent with the
// server's listen mode without an extra knob.
func healthcheckTarget() string {
	if u := os.Getenv("EMBER_HEALTHCHECK_URL"); u != "" {
		return u
	}
	scheme := "http"
	if os.Getenv(envTLSCertFile) != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://127.0.0.1:3627/healthz", scheme)
}

// healthcheckOnce does a single probe against url. The 2 s client timeout
// sits 1 s under the Dockerfile's HEALTHCHECK --timeout=3s so the binary
// can surface a diagnostic on stderr before the daemon kills the probe.
//
// For https targets, two env knobs control verification:
//   - EMBER_HEALTHCHECK_CA_FILE: PEM bundle to add to the trust pool.
//   - EMBER_HEALTHCHECK_INSECURE=1|true: skip verify. Fine on a trusted LAN
//     where the operator can't be bothered to mount a CA bundle.
func healthcheckOnce(url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	if strings.HasPrefix(url, "https://") {
		tlsCfg := &tls.Config{}
		if caFile := os.Getenv("EMBER_HEALTHCHECK_CA_FILE"); caFile != "" {
			pool, err := loadCAPool(caFile)
			if err != nil {
				return fmt.Errorf("healthcheck CA file %s: %w", caFile, err)
			}
			tlsCfg.RootCAs = pool
		}
		if v := os.Getenv("EMBER_HEALTHCHECK_INSECURE"); v == "1" || v == "true" {
			tlsCfg.InsecureSkipVerify = true
		}
		client.Transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	return nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("no PEM certs found in CA file")
	}
	return pool, nil
}

func runHealthcheck() {
	if err := healthcheckOnce(healthcheckTarget()); err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck %v\n", err)
		os.Exit(1)
	}
}
