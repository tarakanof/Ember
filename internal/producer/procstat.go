package producer

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ProcStat is what the producers need from Linux /proc/<pid>/stat.
type ProcStat struct {
	PPID int
	// Comm is the kernel task name: the executable's base name cut to 15
	// bytes ("ember-claude-producer" reads "ember-claude-pr").
	Comm string
	// StartTicks is the start time in clock ticks after boot; with the pid it
	// identifies one process across pid reuse.
	StartTicks uint64
}

// ParseProcStat parses a /proc/<pid>/stat line. comm is parenthesized and
// may itself contain spaces or ')', so fields are read after the last ')'.
func ParseProcStat(b []byte) (ProcStat, bool) {
	open := bytes.IndexByte(b, '(')
	end := bytes.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return ProcStat{}, false
	}
	f := strings.Fields(string(b[end+1:]))
	// f[0]=state(3) f[1]=ppid(4) ... f[19]=starttime(22)
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

// ReadProcStat reads /proc/<pid>/stat (Linux; BusyBox included, no ps needed).
func ReadProcStat(pid int) (ProcStat, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ProcStat{}, false
	}
	return ParseProcStat(b)
}

// clockTicks is USER_HZ; 100 on every mainstream Linux build (sysconf needs cgo).
const clockTicks = 100

// ParseBootTime reads btime (boot time, unix seconds) from /proc/stat content.
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

// ProcStartTime is pid's wall-clock start time on Linux (1/USER_HZ precision).
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
