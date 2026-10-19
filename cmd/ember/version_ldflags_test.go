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

var dockerfileLdflags = regexp.MustCompile(`-ldflags="([^"]*)"`)

func TestVersion_DockerfileLdflagsReachBinary(t *testing.T) {
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

	bin := buildEmberBinary(t, "-ldflags="+ldflags)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run := exec.CommandContext(ctx, bin, "version")
	run.Env = append(isolatedGoRunEnv(t), "CONFIG_PATH=/nonexistent/awtrix.json")
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
