package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFile_ReadAndPreserveOnRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "producer.env")
	original := `# top comment
STATUS_SOURCE=dt-mbp
STATUS_SERVER_URL=http://localhost:8080
# token comment
STATUS_TOKEN=secret
UNKNOWN_KEY=preserved

STATUS_HEARTBEAT_TTL_HOURS=6
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := readEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.get("STATUS_SOURCE") != "dt-mbp" {
		t.Errorf("source = %q", rec.get("STATUS_SOURCE"))
	}
	if rec.get("UNKNOWN_KEY") != "preserved" {
		t.Errorf("unknown key not parsed")
	}
	// Round-trip with no mutation = byte identical
	out := rec.serialize()
	if out != original {
		t.Errorf("round-trip mismatch:\n--got--\n%s\n--want--\n%s", out, original)
	}
}

func TestEnvFile_SetPreservesOrderAndComments(t *testing.T) {
	original := `# comment
STATUS_SOURCE=old
STATUS_TOKEN=t1
UNKNOWN=x
`
	rec, err := parseEnv(strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	rec.set("STATUS_SOURCE", "new")
	out := rec.serialize()
	expected := `# comment
STATUS_SOURCE=new
STATUS_TOKEN=t1
UNKNOWN=x
`
	if out != expected {
		t.Errorf("got:\n%s\nwant:\n%s", out, expected)
	}
}

func TestEnvFile_SetAddsKeyIfMissing(t *testing.T) {
	rec, _ := parseEnv(strings.NewReader(""))
	rec.set("STATUS_SOURCE", "x")
	out := rec.serialize()
	if !strings.Contains(out, "STATUS_SOURCE=x") {
		t.Errorf("set on empty file failed: %q", out)
	}
}

func TestEnvFile_GetReturnsLastDuplicate(t *testing.T) {
	rec, _ := parseEnv(strings.NewReader("STATUS_TOKEN=first\nSTATUS_TOKEN=last\n"))
	if got := rec.get("STATUS_TOKEN"); got != "last" {
		t.Errorf("get with duplicates = %q, want %q (matches producer last-wins)", got, "last")
	}
}

func TestEnvFile_SetUpdatesLastDuplicate(t *testing.T) {
	rec, _ := parseEnv(strings.NewReader("STATUS_TOKEN=first\nSTATUS_TOKEN=stale\nUNKNOWN=x\n"))
	rec.set("STATUS_TOKEN", "")
	out := rec.serialize()
	expected := "STATUS_TOKEN=first\nSTATUS_TOKEN=\nUNKNOWN=x\n"
	if out != expected {
		t.Errorf("set on duplicates didn't update last occurrence:\n--got--\n%s\n--want--\n%s", out, expected)
	}
	// And the get-returns-last contract still holds after the set.
	if got := rec.get("STATUS_TOKEN"); got != "" {
		t.Errorf("after set last to \"\", get = %q, want \"\"", got)
	}
}

func TestWriteEnvAtomic_RoundTripAnd0600(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "producer.env")
	content := "STATUS_SOURCE=mbp\nSTATUS_SERVER_URL=http://localhost:8080\n"
	if err := writeEnvAtomic(path, content); err != nil {
		t.Fatalf("writeEnvAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("content = %q, want %q", got, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteEnvAtomic_RefusesWideConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil { // group/other readable
		t.Fatal(err)
	}
	path := filepath.Join(dir, "producer.env")
	err := writeEnvAtomic(path, "STATUS_SOURCE=x\n")
	if err == nil {
		t.Fatal("expected error for config dir wider than 0700, got nil")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("file was written despite wide dir; want refusal before write")
	}
}
