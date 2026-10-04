package main

import (
	"strconv"

	"github.com/tarakanof/ember/internal/producer"
)

// On Linux the owner walk and liveness read /proc directly: BusyBox ps
// (Alpine) has no -p or lstart, which made every live session look dead.

func procParentComm(pid int) (int, string, bool) {
	st, ok := producer.ReadProcStat(pid)
	if !ok {
		return 0, "", false
	}
	return st.PPID, st.Comm, true
}

// procStart is the process start in clock ticks after boot: unlike ps's
// lstart it needs no ps, and with the pid it survives pid reuse checks.
func procStart(pid int) (string, bool) {
	st, ok := producer.ReadProcStat(pid)
	if !ok {
		return "", false
	}
	return strconv.FormatUint(st.StartTicks, 10), true
}
