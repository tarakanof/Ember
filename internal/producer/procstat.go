package producer

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type ProcStat struct {
	PPID int
	// Comm is the kernel task name cut to 15 bytes.
	Comm       string
	StartTicks uint64
}

func ParseProcStat(b []byte) (ProcStat, bool) {
	open := bytes.IndexByte(b, '(')
	end := bytes.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return ProcStat{}, false
	}
	f := strings.Fields(string(b[end+1:]))
	if len(f) < 20 {
		return ProcStat{}, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	start, err2 := strconv.ParseUint(f[19], 10, 64)
	if err1 != nil || err2 != nil {
		return ProcStat{}, false
	}
	return ProcStat{PPID: ppid, Comm: string(b[open+1 : end]), StartTicks: start}, true
}

func ReadProcStat(pid int) (ProcStat, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ProcStat{}, false
	}
	return ParseProcStat(b)
}

// USER_HZ is 100 on every mainstream Linux build; sysconf needs cgo.
const clockTicks = 100

func ParseBootTime(b []byte) (time.Time, bool) {
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(n, 0), true
			}
		}
	}
	return time.Time{}, false
}

func ProcStartTime(pid int) (time.Time, bool) {
	st, ok := ReadProcStat(pid)
	if !ok {
		return time.Time{}, false
	}
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	boot, ok := ParseBootTime(b)
	if !ok {
		return time.Time{}, false
	}
	return boot.Add(time.Duration(st.StartTicks) * time.Second / clockTicks), true
}
