package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"time"
)

const (
	otaModeManual = "manual"
	otaModeAuto   = "auto"

	otaPhaseIdle        = "idle"
	otaPhaseOffered     = "offered"
	otaPhaseDownloading = "downloading"
	otaPhaseInstalling  = "installing"
	otaPhaseRestarting  = "restarting"
	otaPhaseVerifying   = "verifying"
	otaPhaseDone        = "done"
	otaPhaseFailed      = "failed"
	otaPhaseRolledBack  = "rolled_back"

	otaWaitPomodoro  = "pomodoro"
	otaWaitCoredump  = "coredump"
	otaWaitIdleInput = "idle_input"

	otaURLPrefix      = "/v1/devices/self/firmware/"
	otaNotStarted     = 30 * time.Minute
	otaAutoNotStarted = 24 * time.Hour
	otaBlockedMax     = 16
	otaWriteDeadline  = 10 * time.Minute
)

var (
	errNoRollback      = errors.New("no_rollback_bootloader")
	errOTAInProgress   = errors.New("ota_in_progress")
	errOTANotOffered   = errors.New("this firmware version is not offered to this device")
	errOTAUnknownImage = fmt.Errorf("%w: unknown firmware version", errDeviceBody)

	otaErrorPattern = regexp.MustCompile(`^[a-z0-9_]{1,24}$`)
)

type knobOTA struct {
	Mode       string     `json:"mode,omitempty"`
	Target     string     `json:"target,omitempty"`
	Retry      bool       `json:"retry,omitempty"`
	Blocked    []string   `json:"blocked,omitempty"`
	Phase      string     `json:"phase,omitempty"`
	Error      string     `json:"error,omitempty"`
	From       string     `json:"from,omitempty"`
	Version    string     `json:"version,omitempty"`
	Build      string     `json:"build,omitempty"`
	Size       int        `json:"size,omitempty"`
	Auto       bool       `json:"auto,omitempty"`
	Attempt    int        `json:"attempt,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (o *knobOTA) clone() *knobOTA {
	if o == nil {
		return nil
	}
	c := *o
	c.Blocked = slices.Clone(o.Blocked)
	if o.StartedAt != nil {
		t := *o.StartedAt
		c.StartedAt = &t
	}
	if o.FinishedAt != nil {
		t := *o.FinishedAt
		c.FinishedAt = &t
	}
	return &c
}

func (o knobOTA) mode() string {
	if o.Mode == "" {
		return otaModeManual
	}
	return o.Mode
}

func (o knobOTA) phase() string {
	if o.Phase == "" {
		return otaPhaseIdle
	}
	return o.Phase
}

func otaActive(phase string) bool {
	switch phase {
	case otaPhaseOffered, otaPhaseDownloading, otaPhaseInstalling, otaPhaseRestarting, otaPhaseVerifying:
		return true
	}
	return false
}

func (o knobOTA) servable(version string) bool {
	return o.Version == version && (o.Phase == otaPhaseOffered || o.Phase == otaPhaseDownloading)
}

type knobOTAReport struct {
	Image    string       `json:"image,omitempty"`
	Last     *knobOTALast `json:"last,omitempty"`
	Phase    string       `json:"phase,omitempty"`
	Rollback bool         `json:"rollback"`
	Slot     *int         `json:"slot,omitempty"`
}

type knobOTALast struct {
	Attempt int    `json:"attempt,omitempty"`
	Error   string `json:"error,omitempty"`
	Result  string `json:"result"`
	Version string `json:"version,omitempty"`
}

func (r knobOTAReport) validate() error {
	switch r.Image {
	case "", "valid", "pending_verify", "new", "undefined":
	default:
		return errors.New("image must be valid, pending_verify, new or undefined")
	}
	switch r.Phase {
	case "", "idle", "waiting", "rebooting":
	default:
		return errors.New("phase must be idle, waiting or rebooting")
	}
	if r.Slot != nil && *r.Slot != 0 && *r.Slot != 1 {
		return errors.New("slot must be 0 or 1")
	}
	if l := r.Last; l != nil {
		switch {
		case l.Result != "ok" && l.Result != otaPhaseFailed && l.Result != otaPhaseRolledBack:
			return errors.New("last.result must be ok, failed or rolled_back")
		case l.Error != "" && !otaErrorPattern.MatchString(l.Error):
			return errors.New("last.error must be 1..24 of a-z, 0-9, _")
		case l.Attempt < 0:
			return errors.New("last.attempt must be >= 0")
		case l.Version != "" && !semverPattern.MatchString(l.Version):
			return errors.New("last.version must be a semantic version")
		}
	}
	return nil
}

func (r *knobOTAReport) clone() *knobOTAReport {
	if r == nil {
		return nil
	}
	c := *r
	if r.Last != nil {
		l := *r.Last
		c.Last = &l
	}
	if r.Slot != nil {
		s := *r.Slot
		c.Slot = &s
	}
	return &c
}

type otaOffer struct {
	Attempt int    `json:"attempt"`
	Auto    bool   `json:"auto"`
	Build   string `json:"build"`
	Retry   bool   `json:"retry"`
	SHA256  string `json:"sha256"`
	Size    int    `json:"size"`
	URL     string `json:"url"`
	Version string `json:"version"`
}

type otaInput struct {
	report   deviceCheckin
	now      time.Time
	pomodoro bool
	coredump bool
	target   *firmwareMeta
	auto     *firmwareMeta
}

func stepOTA(o *knobOTA, in otaInput) (*otaOffer, string) {
	applyOTAResult(o, in.report, in.now)
	rep := in.report
	cand, auto := in.target, false
	if o.Target == "" {
		cand, auto = in.auto, true
		if cand != nil && slices.Contains(o.Blocked, cand.Version) {
			cand = nil
		}
	} else if o.Version == o.Target && (o.Phase == otaPhaseFailed || o.Phase == otaPhaseRolledBack) {
		return nil, ""
	}
	if cand == nil {
		if o.Phase == otaPhaseOffered {
			o.Phase = otaPhaseIdle
		}
		return nil, ""
	}
	r := rep.OTA
	if rep.FWBuild == cand.Build {
		if o.Target != "" && r != nil && r.Image == "valid" {
			o.Target, o.Retry = "", false
			if o.Phase == otaPhaseOffered {
				o.Phase = otaPhaseIdle
			}
		}
		return nil, ""
	}
	if r == nil || !r.Rollback || r.Image != "valid" || (r.Phase != "" && r.Phase != "idle") {
		return nil, ""
	}
	if in.pomodoro {
		return nil, otaWaitPomodoro
	}
	if in.coredump {
		return nil, otaWaitCoredump
	}
	if !o.servable(cand.Version) {
		o.Phase, o.Version, o.Build, o.Size, o.Auto = otaPhaseOffered, cand.Version, cand.Build, cand.Size, auto
		o.From, o.Error, o.StartedAt, o.FinishedAt = rep.FW, "", nil, nil
		if l := r.Last; l != nil && l.Attempt > o.Attempt {
			o.Attempt = l.Attempt
		}
		o.Attempt++
	}
	offer := &otaOffer{
		Attempt: o.Attempt,
		Auto:    auto,
		Build:   cand.Build,
		Retry:   o.Retry,
		SHA256:  cand.SHA256,
		Size:    cand.Size,
		URL:     otaURLPrefix + cand.Version,
		Version: cand.Version,
	}
	o.Retry = false
	if auto && o.Phase == otaPhaseOffered {
		return offer, otaWaitIdleInput
	}
	return offer, ""
}

func applyOTAResult(o *knobOTA, rep deviceCheckin, now time.Time) {
	r := rep.OTA
	if o.Version == "" || r == nil || !otaActive(o.Phase) {
		return
	}
	if rep.FWBuild != "" && rep.FWBuild == o.Build {
		if r.Image == "pending_verify" {
			o.Phase = otaPhaseVerifying
			return
		}
		o.Phase, o.Error, o.FinishedAt, o.Retry = otaPhaseDone, "", &now, false
		o.Blocked = slices.DeleteFunc(o.Blocked, func(v string) bool { return v == o.Version })
		if o.Target == o.Version {
			o.Target = ""
		}
		return
	}
	if l := r.Last; l != nil && l.Attempt != 0 && l.Attempt == o.Attempt && l.Version == o.Version &&
		(l.Result == otaPhaseFailed || l.Result == otaPhaseRolledBack) {
		o.Phase, o.Error, o.FinishedAt = l.Result, l.Error, &now
		o.block()
		return
	}
	switch {
	case r.Phase == "waiting" && (o.Phase == otaPhaseOffered || o.Phase == otaPhaseDownloading):
		o.Phase = otaPhaseInstalling
	case r.Phase == "rebooting" && (o.Phase == otaPhaseOffered || o.Phase == otaPhaseDownloading || o.Phase == otaPhaseInstalling):
		o.Phase = otaPhaseRestarting
	}
}

func (r *deviceRegistry) otaSnapshot(id string) (knobOTA, *deviceCheckin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.findKnob(id)
	if d == nil {
		return knobOTA{}, nil, errDeviceNotFound
	}
	c := d.clone()
	var o knobOTA
	if c.OTA != nil {
		o = *c.OTA
	}
	return o, c.LastCheckin, nil
}

func (r *deviceRegistry) updateOTA(id string, fn func(o *knobOTA, last *deviceCheckin) (bool, error)) (knobOTA, *deviceCheckin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writableLocked(); err != nil {
		return knobOTA{}, nil, err
	}
	d := r.state.findKnob(id)
	if d == nil {
		return knobOTA{}, nil, errDeviceNotFound
	}
	var o knobOTA
	if d.OTA != nil {
		o = *d.OTA.clone()
	}
	before, _ := json.Marshal(o)
	last := d.clone().LastCheckin
	bump, err := fn(&o, last)
	if err != nil {
		return knobOTA{}, nil, err
	}
	after, _ := json.Marshal(o)
	if !bump && bytes.Equal(before, after) {
		return o, last, nil
	}
	err = r.mutateLocked(func(st *deviceState) error {
		d := st.findKnob(id)
		d.OTA = o.clone()
		if bump {
			st.Epoch++
		}
		return nil
	})
	return o, last, err
}

func (r *deviceRegistry) otaTargets(version string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.state.Devices {
		if o := d.OTA; o != nil && (o.Target == version || (o.Version == version && otaActive(o.Phase))) {
			return true
		}
	}
	return false
}

func (r *deviceRegistry) otaKeeps(version string) bool {
	if r.otaTargets(version) {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.state.Devices {
		if d.LastCheckin != nil && d.LastCheckin.FW == version {
			return true
		}
	}
	return false
}

type otaProgress struct {
	version string
	bytes   int64
	size    int64
	doneAt  time.Time
}

type otaLive struct {
	mu          sync.Mutex
	progress    map[string]otaProgress
	waiting     map[string]string
	offeredFrom map[string]offerClock
}

type offerClock struct {
	attempt int
	since   time.Time
}

func (l *otaLive) start(device, version string, size int64, fresh bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.progress == nil {
		l.progress = map[string]otaProgress{}
	}
	if p, ok := l.progress[device]; ok && p.version == version && !fresh {
		return
	}
	l.progress[device] = otaProgress{version: version, size: size}
}

func (l *otaLive) offeredSince(device string, attempt int, now time.Time) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.offeredFrom == nil {
		l.offeredFrom = map[string]offerClock{}
	}
	if c, ok := l.offeredFrom[device]; ok && c.attempt == attempt {
		return c.since
	}
	l.offeredFrom[device] = offerClock{attempt: attempt, since: now}
	return now
}

func (l *otaLive) notOffered(device string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.offeredFrom, device)
}

func (l *otaLive) advance(device, version string, pos int64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.progress[device]
	if !ok || p.version != version {
		return
	}
	p.bytes = pos
	if pos >= p.size && p.doneAt.IsZero() {
		p.doneAt = now
	}
	l.progress[device] = p
}

func (l *otaLive) get(device string) (otaProgress, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.progress[device]
	return p, ok
}

func (l *otaLive) setWaiting(device, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.waiting == nil {
		l.waiting = map[string]string{}
	}
	if reason == "" {
		delete(l.waiting, device)
		return
	}
	l.waiting[device] = reason
}

func (l *otaLive) waitingFor(device string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waiting[device]
}

func (l *otaLive) forget(device string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.progress, device)
	delete(l.waiting, device)
	delete(l.offeredFrom, device)
}

type otaStatus struct {
	Mode        string      `json:"mode"`
	Target      *string     `json:"target"`
	Version     *string     `json:"version"`
	Phase       string      `json:"phase"`
	ProgressPct *int        `json:"progress_pct"`
	Bytes       *int64      `json:"bytes"`
	Size        *int        `json:"size"`
	From        *string     `json:"from"`
	Error       *string     `json:"error"`
	StartedAt   *time.Time  `json:"started_at"`
	FinishedAt  *time.Time  `json:"finished_at"`
	Blocked     []string    `json:"blocked"`
	Running     *otaRunning `json:"running"`
	Available   *string     `json:"available"`
	WaitingFor  *string     `json:"waiting_for"`
}

type otaRunning struct {
	FW       string  `json:"fw"`
	Build    *string `json:"build"`
	Slot     *int    `json:"slot"`
	Image    *string `json:"image"`
	Rollback bool    `json:"rollback"`
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func secondUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC().Truncate(time.Second)
	return &u
}

func (a *App) otaStatus(device string, o knobOTA, last *deviceCheckin) otaStatus {
	st := otaStatus{
		Mode:       o.mode(),
		Target:     nonEmpty(o.Target),
		Version:    nonEmpty(o.Version),
		Phase:      o.phase(),
		From:       nonEmpty(o.From),
		Error:      nonEmpty(o.Error),
		StartedAt:  secondUTC(o.StartedAt),
		FinishedAt: secondUTC(o.FinishedAt),
		Blocked:    slices.Clone(o.Blocked),
		WaitingFor: nonEmpty(a.ota.waitingFor(device)),
	}
	if st.Blocked == nil {
		st.Blocked = []string{}
	}
	if o.Version != "" && o.Size > 0 {
		size := o.Size
		st.Size = &size
	}
	if p, ok := a.ota.get(device); ok && p.version == o.Version && o.Version != "" {
		b, pct := p.bytes, 0
		if p.size > 0 {
			pct = int(min(100, b*100/p.size))
		}
		st.Bytes, st.ProgressPct = &b, &pct
		if o.Phase == otaPhaseDownloading && !p.doneAt.IsZero() && (last == nil || last.SeenAt.Before(p.doneAt)) {
			st.Phase = otaPhaseRestarting
		}
	}
	if last != nil {
		run := &otaRunning{FW: last.FW, Build: nonEmpty(last.FWBuild)}
		if r := last.OTA; r != nil {
			run.Slot, run.Image, run.Rollback = r.Slot, nonEmpty(r.Image), r.Rollback
		}
		st.Running = run
		if a.knobFW != nil {
			if m, ok := a.knobFW.newestAbove(last.FW, nil); ok {
				st.Available = &m.Version
			}
		}
	}
	return st
}

func (o *knobOTA) block() {
	if slices.Contains(o.Blocked, o.Version) {
		return
	}
	o.Blocked = append(o.Blocked, o.Version)
	if len(o.Blocked) > otaBlockedMax {
		o.Blocked = o.Blocked[len(o.Blocked)-otaBlockedMax:]
	}
}
