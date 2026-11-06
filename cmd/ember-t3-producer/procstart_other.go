//go:build !darwin && !linux

package main

import "time"

func processStart(int) (time.Time, bool) { return time.Time{}, false }
