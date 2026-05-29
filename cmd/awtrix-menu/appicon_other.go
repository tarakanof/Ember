//go:build !darwin

package main

// applyAppIcon is a no-op off darwin (keeps the package cross-building).
func applyAppIcon(palette string) {}
