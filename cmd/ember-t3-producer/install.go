package main

import (
	"fmt"
	"os"

	"github.com/tarakanof/ember/internal/producer"
)

const launchAgentLabel = "com.ember.t3"

var service = producer.Service{
	Label:        launchAgentLabel,
	Unit:         "ember-t3-producer",
	Description:  "Ember T3 Code producer (polls T3 thread state, reports status)",
	Unquarantine: true,
}

func runInstall(args []string) {
	if err := install(); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		os.Exit(1)
	}
	fmt.Println("Install complete. The T3 Code producer daemon is now running.")
	printSetupHints(args, true)
}

func runConfigure(args []string) {
	if err := configure(); err != nil {
		fmt.Fprintln(os.Stderr, "configure failed:", err)
		os.Exit(1)
	}
	fmt.Println("Configure complete.")
	printSetupHints(args, false)
}

func install() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := service.CheckInstall(home); err != nil {
		return err
	}
	if err := configure(); err != nil {
		return err
	}
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	return service.Install(home, binPath)
}

func configure() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return producer.Configure(home)
}

func printSetupHints(args []string, installing bool) {
	if cfg, err := loadConfig(); err == nil {
		producer.PrintSetupHintsFor(os.Stdout, cfg.Common, args, installing)
	}
}
