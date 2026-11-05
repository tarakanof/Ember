package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/printer"
)

type printEventDTO struct {
	ID    uint64            `json:"id"`
	Kind  printer.EventKind `json:"kind"`
	At    int64             `json:"at"`
	JobID int               `json:"job_id,omitempty"`
}

type printInfoDTO struct {
	Name       string `json:"name"`
	Host       string `json:"host,omitempty"`
	URL        string `json:"url,omitempty"`
	Discovered bool   `json:"discovered"`
	Hostname   string `json:"hostname,omitempty"`
	Firmware   string `json:"firmware,omitempty"`
}

type printStateDTO struct {
	Enabled      bool            `json:"enabled"`
	Phase        printer.Phase   `json:"phase"`
	LastError    string          `json:"last_error,omitempty"`
	PrinterState string          `json:"printer_state,omitempty"`
	JobID        int             `json:"job_id,omitempty"`
	Progress     *int            `json:"progress,omitempty"`
	EndsAt       *int64          `json:"ends_at,omitempty"`
	StartedAt    *int64          `json:"started_at,omitempty"`
	ChangeAt     *int64          `json:"change_at,omitempty"`
	Nozzle       *int            `json:"nozzle,omitempty"`
	NozzleTarget *int            `json:"nozzle_target,omitempty"`
	Bed          *int            `json:"bed,omitempty"`
	BedTarget    *int            `json:"bed_target,omitempty"`
	Name         string          `json:"name,omitempty"`
	Material     string          `json:"material,omitempty"`
	ThumbVersion string          `json:"thumb_version,omitempty"`
	HoldUntil    *int64          `json:"hold_until,omitempty"`
	Event        *printEventDTO  `json:"event,omitempty"`
	Events       []printEventDTO `json:"events"`
	UpdatedAt    *int64          `json:"updated_at,omitempty"`
	Printer      printInfoDTO    `json:"printer"`
}

func unixOrNil(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := t.Unix()
	return &v
}

func degrees(v float64) *int {
	d := int(math.Round(v))
	return &d
}

func printEvent(e printer.Event) printEventDTO {
	return printEventDTO{ID: e.ID, Kind: e.Kind, At: e.At.Unix(), JobID: e.JobID}
}

func printState(s printer.Snapshot) printStateDTO {
	out := printStateDTO{
		Enabled:   s.Enabled,
		Phase:     s.Phase,
		LastError: string(s.LastError),
		Events:    []printEventDTO{},
		UpdatedAt: unixOrNil(s.UpdatedAt),
		Printer: printInfoDTO{Name: s.Printer.Name, Host: s.Printer.Host, URL: s.Printer.URL, Discovered: s.Printer.Discovered,
			Hostname: s.Printer.Hostname, Firmware: s.Printer.Firmware},
	}
	for _, e := range s.Events {
		out.Events = append(out.Events, printEvent(e))
	}
	if s.Event != nil {
		e := printEvent(*s.Event)
		out.Event = &e
	}
	switch s.Phase {
	case printer.PhaseOff, printer.PhaseOffline:
		return out
	}
	out.PrinterState = s.PrinterState
	out.Nozzle, out.NozzleTarget = degrees(s.Nozzle), degrees(s.NozzleTarget)
	out.Bed, out.BedTarget = degrees(s.Bed), degrees(s.BedTarget)
	if s.Phase == printer.PhaseIdle {
		return out
	}
	progress := s.Progress
	out.JobID = s.JobID
	out.Progress = &progress
	out.EndsAt, out.StartedAt, out.ChangeAt = unixOrNil(s.EndsAt), unixOrNil(s.StartedAt), unixOrNil(s.ChangeAt)
	out.Name, out.Material, out.ThumbVersion = s.Name, s.Material, s.ThumbVersion
	out.HoldUntil = unixOrNil(s.HoldUntil)
	return out
}

func (a *App) handlePrintState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, printState(a.printer.State.At(time.Now())))
}

func (a *App) handlePrintConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.printer)
}

func (a *App) handlePrintConfigPut(w http.ResponseWriter, r *http.Request) {
	if c, ok := serveSettingPut(a, w, r, a.settings.printer); ok {
		writeJSON(w, http.StatusOK, c)
	}
}

type printThumbCache struct {
	mu     sync.Mutex
	ver    string
	thumbs printer.Thumbs
	err    error
}

func (c *printThumbCache) get(data []byte, ver string) (printer.Thumbs, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ver != ver {
		c.thumbs, c.err = printer.BakeThumbs(data)
		c.ver = ver
	}
	return c.thumbs, c.err
}

func (a *App) printThumbsFor(s printer.Snapshot) (printer.Thumbs, string, bool) {
	data, ver := a.printer.State.Thumbnail()
	if !s.Enabled || s.ThumbVersion == "" || ver != s.ThumbVersion || len(data) == 0 {
		return printer.Thumbs{}, "", false
	}
	version := printThumbVersion(ver)
	th, err := a.printThumbs.get(data, version)
	if err != nil {
		if a.printThumbFailed.changed("print", version) {
			a.logger.Warn("print thumbnail bake failed", "thumb_version", version, "err", err)
		}
		return printer.Thumbs{}, "", false
	}
	return th, version, true
}

func (a *App) handlePrintThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := printer.ThumbKind(q.Get("kind"))
	if kind != printer.ThumbSolid && kind != printer.ThumbGhost {
		writeError(w, http.StatusBadRequest, printer.ErrThumbKind)
		return
	}
	th, version, ok := a.printThumbsFor(a.printer.State.At(a.viewNow()))
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no print thumbnail"))
		return
	}
	body, _ := th.Kind(kind)
	etag := fmt.Sprintf(`"%s-%s-%d"`, version, kind, printer.ThumbSize)
	h := w.Header()
	h.Set("ETag", etag)
	if v := q.Get("v"); v != "" && v == version {
		h.Set("Cache-Control", "public, max-age=86400, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "image/jpeg")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
