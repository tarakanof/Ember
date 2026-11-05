package main

import (
	"fmt"
	"hash/fnv"
	"time"
)

const printFeedRoute = "print"

func (d printStateDTO) etag() string {
	progress := -1
	if d.Progress != nil {
		progress = *d.Progress
	}
	event := uint64(0)
	if d.Event != nil {
		event = d.Event.ID
	}
	p := d.Printer
	h := fnv.New64a()
	fmt.Fprintf(h, "%t|%s|%d|%s|%d|%d|%q|%q|%q|%t|%q|%q", d.Enabled, d.Phase, progress, d.LastError, event, d.JobID,
		p.Name, p.Host, p.URL, p.Discovered, p.Hostname, p.Firmware)
	return fnvETag(h.Sum64())
}

func (a *App) printStateReply() ([]byte, string, error) {
	d := printState(a.printer.State.At(time.Now()))
	body, err := jsonLine(d)
	return body, d.etag(), err
}
