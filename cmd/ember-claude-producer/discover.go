package main

import (
	"os"

	"github.com/tarakanof/ember/internal/producer"
)

func runDiscover() {
	cfg, _ := loadConfig()
	home, _ := os.UserHomeDir()
	producer.PrintDiscover(os.Stdout, cfg.ServerConfigured, cfg.ServerInstance, home)
}
