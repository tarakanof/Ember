//go:build !darwin && !linux

package main

import (
	"os"
	"time"
)

func changeTime(os.FileInfo) time.Time { return time.Time{} }
