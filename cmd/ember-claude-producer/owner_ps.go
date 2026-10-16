//go:build !linux

package main

import (
	"context"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func procParentComm(pid int) (int, string, bool) {
	return procParentCommCtx(context.Background(), pid)
}

func procParentCommCtx(ctx context.Context, pid int) (int, string, bool) {
	out, err := psOutput(ctx, "-o", "ppid=,comm=", "-p", strconv.Itoa(pid))
	if err != nil {
		return 0, "", false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return 0, "", false
	}
	ppid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", false
	}
	return ppid, strings.Join(fields[1:], " "), true
}

func procStart(pid int) (string, bool) {
	out, err := psOutput(context.Background(), "-o", "lstart=", "-p", strconv.Itoa(pid))
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false
	}
	return s, true
}

func procArgv(ctx context.Context, pid int) ([]string, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, false
	}
	return parseProcArgs2(b)
}
