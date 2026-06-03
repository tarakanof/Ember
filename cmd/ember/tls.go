package main

import (
	"crypto/tls"
	"fmt"
	"os"
)

const (
	envTLSCertFile = "EMBER_TLS_CERT_FILE"
	envTLSKeyFile  = "EMBER_TLS_KEY_FILE"
)

// tlsBundle holds the resolved TLS state for the server.
type tlsBundle struct {
	enabled bool
	cert    tls.Certificate // valid only when enabled
}

// readTLSEnv resolves TLS configuration from the environment. Either both
// EMBER_TLS_CERT_FILE and EMBER_TLS_KEY_FILE must be set (HTTPS), or
// neither (HTTP). Exactly one set is a startup error. When both are set,
// the cert/key pair is parsed eagerly so a malformed cert fails main()
// with a useful slog.Error rather than the server goroutine.
//
// "Bad cert" means "fails tls.LoadX509KeyPair": malformed PEM, unreadable
// file, key/cert mismatch. It does NOT validate expiry, SAN coverage, or
// trust chain — those are the operator's responsibility.
func readTLSEnv() (tlsBundle, error) {
	cert := os.Getenv(envTLSCertFile)
	key := os.Getenv(envTLSKeyFile)
	switch {
	case cert == "" && key == "":
		return tlsBundle{enabled: false}, nil
	case cert == "" || key == "":
		return tlsBundle{}, fmt.Errorf("TLS misconfigured: one of %s/%s set, the other empty",
			envTLSCertFile, envTLSKeyFile)
	}
	loaded, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return tlsBundle{}, fmt.Errorf("TLS load %s + %s: %w", cert, key, err)
	}
	return tlsBundle{enabled: true, cert: loaded}, nil
}
