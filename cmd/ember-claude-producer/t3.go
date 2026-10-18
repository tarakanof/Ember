package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sdkEntrypoint = "sdk-ts"
	t3ProbeBudget = 250 * time.Millisecond
)

type t3Probe struct {
	getenv func(string) string
	ppid   int
	info   func(ctx context.Context, pid int) (ppid int, comm string, ok bool)
	args   func(ctx context.Context, pid int) ([]string, bool)
}

var newT3Probe = func() t3Probe {
	return t3Probe{getenv: os.Getenv, ppid: os.Getppid(), info: procParentCommCtx, args: procArgv}
}

func (p t3Probe) spawnedByT3(ctx context.Context) bool {
	if p.getenv("CLAUDE_CODE_ENTRYPOINT") != sdkEntrypoint && p.getenv("CLAUDE_AGENT_SDK_VERSION") == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, t3ProbeBudget)
	defer cancel()
	info := func(pid int) (int, string, bool) { return p.info(ctx, pid) }
	owner, ownerParent := walkPastShells(p.ppid, info)
	if owner <= 0 || ownerParent <= 1 {
		return false
	}
	launcher, _ := walkPastShells(ownerParent, info)
	if launcher <= 0 {
		return false
	}
	argv, ok := p.args(ctx, launcher)
	return ok && isT3ServerArgv(argv)
}

func isT3ServerArgv(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	if isT3Executable(argv[0]) {
		return true
	}
	for i := 1; i+1 < len(argv); i++ {
		if strings.HasSuffix(argv[i], "/app.asar/apps/server/dist/bin.mjs") && argv[i+1] == "--bootstrap-fd" {
			return true
		}
	}
	switch filepath.Base(argv[0]) {
	case "node", "bun":
		for _, a := range argv[1:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			return isT3Executable(a) || strings.HasSuffix(a, "/node_modules/t3/dist/bin.mjs")
		}
	}
	return false
}

func isT3Executable(path string) bool {
	return filepath.Base(path) == "t3"
}

func parseProcArgs2(b []byte) ([]string, bool) {
	if len(b) < 4 {
		return nil, false
	}
	argc := int(binary.LittleEndian.Uint32(b))
	rest := b[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil, false
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	argv := make([]string, 0, argvCap(argc, rest))
	for len(argv) < argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, false
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[end+1:]
	}
	return argv, argc > 0
}

func argvCap(argc int, rest []byte) int {
	return min(argc, len(rest))
}

func reapT3Session(ctx context.Context, cfg Config, sessionID string) {
	dir, err := stateDir()
	if err != nil {
		return
	}
	markerP := markerPath(dir, sessionID)
	if _, err := os.Stat(markerP); err != nil {
		return
	}
	handleDelete(ctx, cfg, sessionID, markerP, lockPath(dir, sessionID))
}
