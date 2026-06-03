package producer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadEnvFile_Missing(t *testing.T) {
	got, err := ReadEnvFile(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestReadEnvFile_RejectsWidePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "producer.env")
	if err := os.WriteFile(path, []byte("EMBER_SOURCE=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnvFile(path); err == nil {
		t.Error("0644 file should error")
	}
}

func TestReadEnvFile_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.env")
	if err := os.WriteFile(target, []byte("EMBER_SOURCE=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "producer.env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnvFile(link); err == nil {
		t.Error("symlink should error")
	}
}

func TestReadEnvFile_ParsesQuotesAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "producer.env")
	content := "# comment\nEMBER_SOURCE=mbp\nEMBER_SERVER_URL=\"http://h:8080\"\n\nEMBER_TOKEN=abc\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"EMBER_SOURCE": "mbp", "EMBER_SERVER_URL": "http://h:8080", "EMBER_TOKEN": "abc"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}
