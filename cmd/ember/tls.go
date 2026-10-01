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

type tlsBundle struct {
	enabled bool
	cert    tls.Certificate
}

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
