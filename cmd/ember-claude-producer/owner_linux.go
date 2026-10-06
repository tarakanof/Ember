package main

import (
	"strconv"

	"github.com/tarakanof/ember/internal/producer"
)

func procParentComm(pid int) (int, string, bool) {
	st, ok := producer.ReadProcStat(pid)
	if !ok {
		return 0, "", false
	}
	return st.PPID, st.Comm, true
}

func procStart(pid int) (string, bool) {
	st, ok := producer.ReadProcStat(pid)
	if !ok {
		return "", false
	}
	return strconv.FormatUint(st.StartTicks, 10), true
}
