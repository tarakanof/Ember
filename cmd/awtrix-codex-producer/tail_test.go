package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadNewLines_AdvancesAndSkipsPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.jsonl")
	if err := os.WriteFile(path, []byte("line1\nline2\npartial"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, off, err := readNewLines(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || string(lines[0]) != "line1" || string(lines[1]) != "line2" {
		t.Fatalf("lines = %v", lines)
	}
	if off != 12 { // "line1\nline2\n" = 12 bytes; "partial" not consumed
		t.Fatalf("offset = %d, want 12", off)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("-rest\n")
	f.Close()
	lines, off2, err := readNewLines(path, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || string(lines[0]) != "partial-rest" {
		t.Fatalf("second read lines = %v", lines)
	}
	if off2 != 25 {
		t.Fatalf("offset2 = %d, want 25", off2)
	}
}

func TestReadNewLines_NoNewBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.jsonl")
	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, off, err := readNewLines(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 || off != 2 {
		t.Fatalf("lines=%v off=%d, want empty/2", lines, off)
	}
}

func TestBuildStatusRequest(t *testing.T) {
	pct, rw := 50, 42
	cfg := Config{Source: "mbp", SourceColor: "#aa66ff"}
	req := buildStatusRequest(cfg, "u-1", derived{state: "running", message: "hi", contextPct: &pct, rateWindowPct: &rw})
	if req.Source != "mbp" || req.Tool != "codex" || req.Session != "u-1" || req.State != "running" || req.Message != "hi" {
		t.Fatalf("req = %+v", req)
	}
	if req.ContextPct == nil || *req.ContextPct != 50 || req.RateWindowPct == nil || *req.RateWindowPct != 42 {
		t.Fatalf("metrics = %+v", req)
	}
	if req.SourceColor == nil || *req.SourceColor != "#aa66ff" {
		t.Fatalf("source color = %v", req.SourceColor)
	}
}

func TestBuildStatusRequest_NoColorWhenEmpty(t *testing.T) {
	req := buildStatusRequest(Config{Source: "mbp"}, "u-1", derived{state: "done"})
	if req.SourceColor != nil {
		t.Errorf("SourceColor should be nil when cfg empty, got %v", *req.SourceColor)
	}
}

func TestBuildStatusRequest_SetsContextNumber(t *testing.T) {
	on := buildStatusRequest(Config{Source: "mbp", ContextNumberEnabled: true}, "u1", derived{state: "running"})
	if !on.ContextNumber {
		t.Error("ContextNumber should be true when enabled")
	}
	off := buildStatusRequest(Config{Source: "mbp"}, "u1", derived{state: "running"})
	if off.ContextNumber {
		t.Error("ContextNumber should be false by default")
	}
}

func TestBuildStatusRequest_SetsRateBottomBar(t *testing.T) {
	on := buildStatusRequest(Config{Source: "mbp", RateBottomBarEnabled: true}, "u1", derived{state: "running"})
	if !on.RateBottomBar {
		t.Error("RateBottomBar should be true when enabled")
	}
	off := buildStatusRequest(Config{Source: "mbp"}, "u1", derived{state: "running"})
	if off.RateBottomBar {
		t.Error("RateBottomBar should be false by default")
	}
}
