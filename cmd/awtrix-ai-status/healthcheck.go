package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

const defaultHealthcheckURL = "http://127.0.0.1:8080/healthz"

// healthcheckTarget returns the URL the in-image healthcheck should probe.
// STATUS_HEALTHCHECK_URL overrides; otherwise the default points at the
// container's loopback interface on the server's default port.
func healthcheckTarget() string {
	if u := os.Getenv("STATUS_HEALTHCHECK_URL"); u != "" {
		return u
	}
	return defaultHealthcheckURL
}

// healthcheckOnce does a single probe against url. The 2 s client timeout
// sits 1 s under the Dockerfile's HEALTHCHECK --timeout=3s so the binary
// can surface a diagnostic on stderr before the daemon kills the probe.
func healthcheckOnce(url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
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

func runHealthcheck() {
	if err := healthcheckOnce(healthcheckTarget()); err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck %v\n", err)
		os.Exit(1)
	}
}
