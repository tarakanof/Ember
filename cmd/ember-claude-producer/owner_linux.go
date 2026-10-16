package main

import (
	"context"
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

func procParentCommCtx(ctx context.Context, pid int) (int, string, bool) {
	if ctx.Err() != nil {
		return 0, "", false
	}
	return procParentComm(pid)
}

func procArgv(ctx context.Context, pid int) ([]string, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(b) == 0 {
		return nil, false
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"), true
}
