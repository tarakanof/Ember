package main

import (
	"bytes"
	"io"
	"os"

	"github.com/tarakanof/ember/internal/producer"
)

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

func buildStatusRequest(cfg Config, uuid string, d derived) producer.StatusRequest {
	req := cfg.StatusRequest("codex", uuid, d.state)
	req.Message = d.message
	req.Activity = d.activity
	req.ContextPct = d.contextPct
	req.RateWindowPct = d.rateWindowPct
	req.RateResetAt = d.rateResetAt
	cfg.Gauges.Apply(&req)
	return req
}
