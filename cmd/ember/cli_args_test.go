package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildEmberBinary(t *testing.T, buildFlags ...string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the server binary; skipped under -short (CI runs the full suite)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	bin := filepath.Join(t.TempDir(), "ember")
	build := goRuns.command(ctx, "go", append(append([]string{"build"}, buildFlags...), "-o", bin, ".")...)
	build.Env = isolatedGoRunEnv(t)
	var out bytes.Buffer
	build.Stdout, build.Stderr = &out, &out
	if err := build.Run(); err != nil {
		t.Fatalf("go build %q: %v\n%s", buildFlags, err, out.String())
	}
	return bin
}

func TestCLI_RejectedArgsExitTwo(t *testing.T) {
	bin := buildEmberBinary(t)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"server flag before subcommand": {[]string{"-print-config", "doctor"}, "flag -print-config before subcommand doctor: -print-config is a server flag and does not combine with a subcommand"},
		"help before subcommand":        {[]string{"-h", "doctor"}, "for doctor's flags run `ember doctor -h`, for the server's `ember -h`"},
		"doctor flag before doctor":     {[]string{"-offline", "doctor"}, "put doctor flags after it (`ember doctor -offline`)"},
		"unknown flag before doctor":    {[]string{"-jsn", "doctor"}, "-jsn is not a doctor flag; see `ember doctor -h`"},
		"flag before flagless command":  {[]string{"-offline", "healthcheck"}, "healthcheck takes no flags"},
		"subcommand after --":           {[]string{"--", "doctor"}, `unexpected argument "doctor"`},
		"unknown subcommand":            {[]string{"doctr"}, `unexpected argument "doctr"`},
		"bool flag value as own token":  {[]string{"-print-config", "false"}, `unexpected argument "false"`},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, tc.args...)
			cmd.Env = isolatedGoRunEnv(t)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("%q: err = %v (ctx %v), want exit status 2\nstdout: %s\nstderr: %s", tc.args, err, ctx.Err(), stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("%q: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
			}
		})
	}
}
