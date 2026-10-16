package main

import (
	"os"
	"path/filepath"
	"strings"
)

const sdkEntrypoint = "sdk-ts"

type t3Probe struct {
	getenv func(string) string
	ppid   int
	info   func(int) (ppid int, comm string, ok bool)
	args   func(int) (string, bool)
}

var newT3Probe = func() t3Probe {
	return t3Probe{getenv: os.Getenv, ppid: os.Getppid(), info: procParentComm, args: procArgs}
}

func (p t3Probe) spawnedByT3() bool {
	if p.getenv("CLAUDE_CODE_ENTRYPOINT") != sdkEntrypoint {
		return false
	}
	owner := resolveOwner(p.ppid, p.info)
	if owner <= 0 {
		return false
	}
	ownerParent, _, ok := p.info(owner)
	if !ok {
		return false
	}
	launcher := resolveOwner(ownerParent, p.info)
	if launcher <= 0 {
		return false
	}
	args, ok := p.args(launcher)
	return ok && isT3ServerArgs(args)
}

func isT3ServerArgs(args string) bool {
	args = strings.ReplaceAll(args, `\`, "/")
	if strings.Contains(args, "/apps/server/dist/bin.mjs") || strings.Contains(args, "/node_modules/t3/dist/bin.mjs") {
		return true
	}
	fields := strings.Fields(args)
	for _, f := range fields[:min(2, len(fields))] {
		if filepath.Base(f) == "t3" {
			return true
		}
	}
	return false
}
