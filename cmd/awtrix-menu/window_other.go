//go:build !darwin

package main

// openSettingsWindow is a no-op on non-darwin platforms; the real window
// lives in window_darwin.go. Keeps the package compiling cross-platform.
func openSettingsWindow(envPath string) {}

// openPomodoroWindow is a no-op on non-darwin platforms; the real window lives
// in pomodoro_window_darwin.go.
func openPomodoroWindow(envPath string) {}
