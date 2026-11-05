package printer

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Phase string

const (
	PhaseOff       Phase = "off"
	PhaseOffline   Phase = "offline"
	PhaseIdle      Phase = "idle"
	PhaseHeating   Phase = "heating"
	PhasePreparing Phase = "preparing"
	PhasePrinting  Phase = "printing"
	PhaseFinishing Phase = "finishing"
	PhasePaused    Phase = "paused"
	PhaseAttention Phase = "attention"
	PhaseFinished  Phase = "finished"
	PhaseStopped   Phase = "stopped"
	PhaseError     Phase = "error"
)

type ErrorCode string

const (
	ErrCodeNone          ErrorCode = ""
	ErrCodeNoPassword    ErrorCode = "no_password"
	ErrCodeWrongPassword ErrorCode = "wrong_password"
	ErrCodeUnreachable   ErrorCode = "unreachable"
	ErrCodeNotFound      ErrorCode = "not_found"
)

type EventKind string

const (
	EventStarted   EventKind = "started"
	EventAttention EventKind = "attention"
	EventFinished  EventKind = "finished"
	EventStopped   EventKind = "stopped"
	EventError     EventKind = "error"
)

const (
	EventTTL        = 30 * time.Minute
	StoppedHold     = 10 * time.Minute
	AnchorTolerance = 60 * time.Second
	heatingMargin   = 3.0
	maxEvents       = 32
	MaxNameRunes    = 32
)

type Event struct {
	ID    uint64    `json:"id"`
	Kind  EventKind `json:"kind"`
	At    time.Time `json:"at"`
	JobID int       `json:"job_id,omitempty"`
}

type Info struct {
	Name       string
	URL        string
	Host       string
	Hostname   string
	Firmware   string
	Discovered bool
}

type Snapshot struct {
	Enabled      bool
	Phase        Phase
	PrinterState string
	JobID        int
	Progress     int
	EndsAt       time.Time
	StartedAt    time.Time
	ChangeAt     time.Time
	Nozzle       float64
	NozzleTarget float64
	Bed          float64
	BedTarget    float64
	Name         string
	Material     string
	ThumbVersion string
	Event        *Event
	Events       []Event
	HoldUntil    time.Time
	LastError    ErrorCode
	UpdatedAt    time.Time
	Printer      Info
}

type State struct {
	mu sync.Mutex

	enabled  bool
	phase    Phase
	lastErr  ErrorCode
	info     Info
	doneHold time.Duration

	seen      bool
	prevPhase Phase
	rawState  string
	updatedAt time.Time

	jobID     int
	startedID int
	progress  float64
	endsAt    time.Time
	startedAt time.Time
	changeAt  time.Time
	nozzle    float64
	nozzleT   float64
	bed       float64
	bedT      float64

	name     string
	material string
	thumbRef string
	thumb    []byte
	thumbVer string

	events     []Event
	lastID     uint64
	finishedAt time.Time
	stoppedAt  time.Time
}

func NewState() *State {
	return &State{phase: PhaseOff, doneHold: DefaultDoneHoldMin * time.Minute}
}

func (s *State) At(now time.Time) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	out := Snapshot{
		Enabled:      s.enabled,
		Phase:        s.phase,
		PrinterState: s.rawState,
		JobID:        s.jobID,
		EndsAt:       s.endsAt,
		StartedAt:    s.startedAt,
		ChangeAt:     s.changeAt,
		Nozzle:       s.nozzle,
		NozzleTarget: s.nozzleT,
		Bed:          s.bed,
		BedTarget:    s.bedT,
		Name:         s.name,
		Material:     s.material,
		ThumbVersion: s.thumbVer,
		LastError:    s.lastErr,
		UpdatedAt:    s.updatedAt,
		Printer:      s.info,
	}
	switch s.phase {
	case PhaseOff, PhaseOffline, PhaseIdle:
		out.JobID, out.EndsAt, out.StartedAt, out.ChangeAt = 0, time.Time{}, time.Time{}, time.Time{}
		out.Name, out.Material, out.ThumbVersion = "", "", ""
	case PhaseHeating, PhasePreparing:
	case PhaseFinished:
		out.Progress = 100
	default:
		out.Progress = wholePercent(s.progress)
	}
	if len(s.events) > 0 {
		out.Events = append([]Event(nil), s.events...)
		e := out.Events[len(out.Events)-1]
		out.Event = &e
	}
	out.HoldUntil = s.holdUntilLocked(now)
	return out
}

func wholePercent(p float64) int {
	if math.IsNaN(p) || p <= 0 {
		return 0
	}
	if p >= 100 {
		return 100
	}
	return int(p)
}

func (s *State) holdUntilLocked(now time.Time) time.Time {
	var at time.Time
	hold := s.doneHold
	switch s.phase {
	case PhaseFinished:
		at = s.finishedAt
	case PhaseStopped:
		at, hold = s.stoppedAt, StoppedHold
	}
	if at.IsZero() {
		return time.Time{}
	}
	if until := at.Add(hold); now.Before(until) {
		return until
	}
	return time.Time{}
}

func (s *State) Thumbnail() ([]byte, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.thumb, s.thumbVer
}

func (s *State) pruneLocked(now time.Time) {
	cut := 0
	for cut < len(s.events) && now.Sub(s.events[cut].At) > EventTTL {
		cut++
	}
	if cut > 0 {
		s.events = append(s.events[:0:0], s.events[cut:]...)
	}
}

func (s *State) emitLocked(now time.Time, kind EventKind) {
	id := uint64(now.UnixMilli())
	if id <= s.lastID {
		id = s.lastID + 1
	}
	s.lastID = id
	switch kind {
	case EventFinished:
		s.finishedAt = now
	case EventStopped:
		s.stoppedAt = now
	}
	s.events = append(s.events, Event{ID: id, Kind: kind, At: now, JobID: s.jobID})
	if len(s.events) > maxEvents {
		s.events = s.events[len(s.events)-maxEvents:]
	}
}

func (s *State) configure(enabled bool, doneHold time.Duration, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = enabled
	s.doneHold = doneHold
	s.info.Name = name
}

func (s *State) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen, s.prevPhase, s.rawState, s.updatedAt = false, "", "", time.Time{}
	s.jobID, s.startedID, s.progress = 0, 0, 0
	s.endsAt, s.startedAt, s.changeAt = time.Time{}, time.Time{}, time.Time{}
	s.nozzle, s.nozzleT, s.bed, s.bedT = 0, 0, 0, 0
	s.name, s.material, s.thumbRef, s.thumb, s.thumbVer = "", "", "", nil, ""
	s.events, s.finishedAt, s.stoppedAt = nil, time.Time{}, time.Time{}
	s.info = Info{Name: s.info.Name}
}

func (s *State) setOff(code ErrorCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase, s.lastErr = PhaseOff, code
}

func (s *State) setOffline(code ErrorCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase, s.lastErr = PhaseOffline, code
}

func (s *State) setTarget(url, host string, discovered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.URL != url {
		s.info.Hostname, s.info.Firmware = "", ""
	}
	s.info.URL, s.info.Host, s.info.Discovered = url, host, discovered
}

func (s *State) setVersion(v VersionDoc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info.Hostname, s.info.Firmware = v.Hostname, v.Firmware
}

func DerivePhase(d StatusDoc) Phase {
	switch strings.ToUpper(d.Printer.State) {
	case "PRINTING":
		return printingPhase(d)
	case "BUSY":
		if d.Job != nil {
			return printingPhase(d)
		}
		return PhaseIdle
	case "PAUSED":
		return PhasePaused
	case "ATTENTION":
		return PhaseAttention
	case "FINISHED":
		return PhaseFinished
	case "STOPPED":
		return PhaseStopped
	case "ERROR":
		return PhaseError
	default:
		return PhaseIdle
	}
}

func printingPhase(d StatusDoc) Phase {
	p := 0.0
	if d.Job != nil {
		p = d.Job.Progress
	}
	switch {
	case p >= 100:
		return PhaseFinishing
	case p >= 1:
		return PhasePrinting
	}
	pr := d.Printer
	cold := func(temp, target float64) bool { return target > 0 && temp < target-heatingMargin }
	if cold(pr.TempNozzle, pr.TargetNozzle) || cold(pr.TempBed, pr.TargetBed) {
		return PhaseHeating
	}
	return PhasePreparing
}

func ActiveStatus(d StatusDoc) bool {
	switch strings.ToUpper(d.Printer.State) {
	case "PRINTING", "PAUSED", "ATTENTION":
		return true
	case "BUSY":
		return d.Job != nil
	}
	return false
}

func (s *State) observe(now time.Time, d StatusDoc) (stateChanged, jobChanged bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	phase := DerivePhase(d)
	raw := strings.ToUpper(d.Printer.State)
	stateChanged = !s.seen || raw != s.rawState
	pr := d.Printer
	s.nozzle, s.nozzleT, s.bed, s.bedT = pr.TempNozzle, pr.TargetNozzle, pr.TempBed, pr.TargetBed
	s.rawState, s.updatedAt = raw, now

	if j := d.Job; j != nil {
		if j.ID != s.jobID {
			jobChanged = true
			s.jobID = j.ID
			s.endsAt, s.startedAt, s.changeAt = time.Time{}, time.Time{}, time.Time{}
			s.name, s.material, s.thumbRef, s.thumb, s.thumbVer = "", "", "", nil, ""
		}
		s.progress = j.Progress
		s.endsAt = anchorAfter(s.endsAt, now, j.TimeRemaining)
		s.changeAt = anchorAfter(s.changeAt, now, j.FilamentChangeIn)
		if j.TimePrinting != nil {
			s.startedAt = anchor(s.startedAt, now.Add(-seconds(*j.TimePrinting)))
		}
	} else {
		s.endsAt, s.changeAt = time.Time{}, time.Time{}
	}

	if s.seen {
		s.edgesLocked(now, phase)
	} else if d.Job != nil && (phase != PhaseAttention || jobUnderway(d.Job)) {
		s.startedID = s.jobID
	}
	s.seen = true
	s.prevPhase = phase
	s.phase, s.lastErr = phase, ErrCodeNone
	return stateChanged, jobChanged
}

func jobUnderway(j *StatusJob) bool {
	return j.Progress >= 1 || (j.TimePrinting != nil && *j.TimePrinting > 0)
}

func isRunning(p Phase) bool {
	switch p {
	case PhaseHeating, PhasePreparing, PhasePrinting, PhaseFinishing:
		return true
	}
	return false
}

func (s *State) edgesLocked(now time.Time, phase Phase) {
	if isRunning(phase) && s.jobID != 0 && s.jobID != s.startedID {
		s.startedID = s.jobID
		s.emitLocked(now, EventStarted)
	}
	if phase == s.prevPhase {
		return
	}
	switch phase {
	case PhaseAttention:
		s.emitLocked(now, EventAttention)
	case PhaseFinished:
		s.emitLocked(now, EventFinished)
	case PhaseStopped:
		s.emitLocked(now, EventStopped)
	case PhaseError:
		s.emitLocked(now, EventError)
	}
}

func seconds(v float64) time.Duration {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	return time.Duration(v * float64(time.Second))
}

func anchorAfter(prev, now time.Time, secs *float64) time.Time {
	if secs == nil {
		return time.Time{}
	}
	return anchor(prev, now.Add(seconds(*secs)))
}

func anchor(prev, next time.Time) time.Time {
	if prev.IsZero() {
		return next.Truncate(time.Second)
	}
	if d := next.Sub(prev); d > AnchorTolerance || d < -AnchorTolerance {
		return next.Truncate(time.Second)
	}
	return prev
}

func (s *State) setJob(d *JobDoc) (thumbRef string, fetch bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d == nil {
		s.name, s.material, s.thumbRef, s.thumb, s.thumbVer = "", "", "", nil, ""
		return "", false
	}
	if d.ID != 0 && d.ID != s.jobID {
		s.jobID = d.ID
		s.thumbRef, s.thumb, s.thumbVer = "", nil, ""
	}
	display := d.File.DisplayName
	if display == "" {
		display = d.File.Name
	}
	s.name, s.material = ShortName(display)
	ref := d.File.Refs.Thumbnail
	if ref == "" || ref == s.thumbRef {
		return "", false
	}
	s.thumbRef = ref
	return ref, true
}

func (s *State) setThumb(ref string, b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ref != s.thumbRef {
		return
	}
	s.thumb = b
	if b == nil {
		s.thumbVer = ""
		return
	}
	sum := sha256.Sum256(b)
	s.thumbVer = hex.EncodeToString(sum[:6])
}

func (s *State) forgetThumbRef(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.thumbRef == ref && s.thumb == nil {
		s.thumbRef = ""
	}
}

var (
	gcodeExt     = regexp.MustCompile(`(?i)\.(bgcode|gcode|bgc|gco|g)$`)
	slicerSuffix = regexp.MustCompile(`^(.+?)_(\d+(?:\.\d+)?)n_(\d+(?:\.\d+)?)mm_([A-Za-z0-9+;,\-]+)_([A-Za-z0-9]+)_((?:\d+d)?(?:\d+h)?(?:\d+m)?(?:\d+s)?)$`)
)

func ShortName(display string) (name, material string) {
	base := gcodeExt.ReplaceAllString(strings.TrimSpace(display), "")
	if m := slicerSuffix.FindStringSubmatch(base); m != nil && m[6] != "" {
		base = m[1]
		material = strings.NewReplacer(";", "/", ",", "/").Replace(m[4])
	}
	return truncateRunes(base, MaxNameRunes), material
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
