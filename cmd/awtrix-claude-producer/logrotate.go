package main

import (
	"fmt"
	"os"
)

const (
	logRotateThreshold   = 10 * 1024 * 1024 // 10 MiB
	logRotateGenerations = 5
)

func rotateLogIfLarge(path string, threshold int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < threshold {
		return
	}
	for i := logRotateGenerations - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i)
		to := fmt.Sprintf("%s.%d", path, i+1)
		_ = os.Rename(from, to)
	}
	_ = os.Rename(path, path+".1")
}
