package main

import (
	"fmt"
	"os"
)

func runUninstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: cannot find home dir:", err)
		os.Exit(1)
	}
	if err := service.Uninstall(home, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall:", err)
	}
	fmt.Println("Uninstall complete. producer.env was left in place (shared with the Claude producer).")
}
