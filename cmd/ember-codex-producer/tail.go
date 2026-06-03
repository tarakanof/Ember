package main

import (
	"bytes"
	"io"
	"os"

	"github.com/tarakanof/ember/internal/producer"
)

// readNewLines reads complete (newline-terminated) lines from path starting at
// byte offset, returning the lines (without the trailing '\n') and the new
// offset advanced past only the complete lines. A trailing partial line is left
// unconsumed for a later read.
func readNewLines(path string, offset int64) ([][]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset, err
	}
	var lines [][]byte
	newOffset := offset
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, append([]byte(nil), data[:i]...))
		newOffset += int64(i + 1)
		data = data[i+1:]
	}
	return lines, newOffset, nil
}

// buildStatusRequest assembles the POST body for a Codex session.
func buildStatusRequest(cfg Config, uuid string, d derived) producer.StatusRequest {
	req := producer.StatusRequest{
		Source:        cfg.Source,
		Tool:          "codex",
		Session:       uuid,
		State:         d.state,
		Message:       d.message,
		Activity:      d.activity,
		ContextPct:    d.contextPct,
		RateWindowPct: d.rateWindowPct,
		ContextNumber: cfg.ContextNumberEnabled,
		RateBottomBar: cfg.RateBottomBarEnabled,
		RateResetAt:   d.rateResetAt,
		RateReset:     cfg.RateResetEnabled,
	}
	if cfg.SourceColor != "" {
		sc := cfg.SourceColor
		req.SourceColor = &sc
	}
	return req
}
