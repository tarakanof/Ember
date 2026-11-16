package main

import (
	"bytes"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

type processStats struct {
	goroutines    int
	cpuSeconds    float64
	residentBytes int64
}

func readProcessStats() processStats {
	s := processStats{goroutines: runtime.NumGoroutine()}
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.cpuSeconds = time.Duration(ru.Utime.Nano() + ru.Stime.Nano()).Seconds()
		s.residentBytes = maxRSSBytes(ru.Maxrss)
	}
	if rss, ok := procResidentBytes(); ok {
		s.residentBytes = rss
	}
	return s
}

func maxRSSBytes(maxrss int64) int64 {
	if runtime.GOOS == "darwin" {
		return maxrss
	}
	return maxrss * 1024
}

func procResidentBytes() (int64, bool) {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	f := bytes.Fields(b)
	if len(f) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseInt(string(f[1]), 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * int64(os.Getpagesize()), true
}
