package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// dockerfileLdflags matches the -ldflags="..." argument of the Dockerfile's
// `go build` line. The test reuses that exact string, so the symbol path the
// release image injects into is the one exercised here.
var dockerfileLdflags = regexp.MustCompile(`-ldflags="([^"]*)"`)

// TestVersion_DockerfileLdflagsReachBinary pins the -X symbol path the
// Dockerfile targets (#62). The linker silently ignores an -X target that
// doesn't resolve, so moving `version` out of package main, or editing the
// Dockerfile's -X path without moving the variable, would ship images that
// report "dev" with every other test still green. This builds ./cmd/ember with
// the Dockerfile's own ldflags ($VERSION substituted) and checks the `version`
// subcommand prints the injected value.
func TestVersion_DockerfileLdflagsReachBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the server binary; skipped under -short (CI runs the full suite)")
	}

	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	m := dockerfileLdflags.FindSubmatch(dockerfile)
	if m == nil {
		t.Fatal(`Dockerfile has no -ldflags="..." on its go build line`)
	}
	ldflags := string(m[1])
	if !strings.Contains(ldflags, "-X main.version=$VERSION") {
		t.Fatalf("Dockerfile ldflags %q no longer inject -X main.version=$VERSION; "+
			"update this test and cmd/ember/version.go together with it", ldflags)
	}

	const want = "9.9.9-test"
	ldflags = strings.ReplaceAll(ldflags, "$VERSION", want)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "ember")
	build := exec.CommandContext(ctx, "go", "build", "-ldflags="+ldflags, "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build -ldflags=%q failed: %v\n%s", ldflags, err, out)
	}

	run := exec.CommandContext(ctx, bin, "version")
	// A missing config file makes a broken dispatcher exit before binding :3627.
	run.Env = append(os.Environ(), "CONFIG_PATH=/nonexistent/awtrix.json")
	var stdout, stderr bytes.Buffer
	run.Stdout, run.Stderr = &stdout, &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("%s version failed: %v\nstderr: %s", bin, err, stderr.String())
	}
	if got := stdout.String(); !strings.HasPrefix(got, "ember "+want+" ") {
		t.Errorf("version output %q does not carry the injected %q; the Dockerfile's "+
			"-X target no longer resolves to the version variable", got, want)
	}
}
