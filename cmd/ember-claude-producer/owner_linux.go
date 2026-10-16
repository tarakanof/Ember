package main

import (
	"os"
	"strconv"
	"strings"

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

func procArgs(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(b) == 0 {
		return "", false
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " ")), true
}
