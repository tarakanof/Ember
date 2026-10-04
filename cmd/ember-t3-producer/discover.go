package main

import (
	"os"

	"github.com/tarakanof/ember/internal/producer"
)

// runDiscover browses for the server (EMBER_SERVER_URL empty or "auto"),
// caches the pick for hooks and daemons, and probes it.
func runDiscover() {
	cfg, _ := loadConfig()
	home, _ := os.UserHomeDir()
	producer.PrintDiscover(os.Stdout, cfg.ServerConfigured, cfg.ServerInstance, home)
}
