package main

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/devices"
)

const (
	otaModeAuto = "auto"

	otaWaitPomodoro  = "pomodoro"
	otaWaitCoredump  = "coredump"
	otaWaitIdleInput = "idle_input"

	otaURLPrefix      = "/v1/devices/self/firmware/"
	otaNotStarted     = 30 * time.Minute
	otaAutoNotStarted = 24 * time.Hour
	otaWriteDeadline  = 10 * time.Minute
	otaPutTries       = 3
)

var (
	errNoRollback      = errors.New("no_rollback_bootloader")
	errOTAInProgress   = errors.New("ota_in_progress")
	errOTANotOffered   = errors.New("this firmware version is not offered to this device")
	errOTAUnknownImage = fmt.Errorf("%w: unknown firmware version", devices.ErrBody)
	errFirmwareChanged = errors.New("firmware_changed")
)

type otaInput struct {
	report   devices.Checkin
	now      time.Time
	pomodoro bool
	coredump bool
	target   *firmwareMeta
	auto     *firmwareMeta
}

func stepOTA(o *devices.KnobOTA, in otaInput) (*devices.OTAOffer, string) {
	applyOTAResult(o, in.report, in.now)
	rep := in.report
	cand, auto := in.target, false
	if o.Target == "" {
		cand, auto = in.auto, true
		if cand != nil && slices.Contains(o.Blocked, cand.Version) {
			cand = nil
		}
	} else if o.Version == o.Target && (o.Phase == devices.OTAPhaseFailed || o.Phase == devices.OTAPhaseRolledBack) {
		return nil, ""
	}
	if cand == nil {
		if o.Phase == devices.OTAPhaseOffered {
			o.Phase = devices.OTAPhaseIdle
		}
		return nil, ""
	}
	r := rep.OTA
	if rep.FWBuild == cand.Build {
		if o.Target != "" && r != nil && r.Image == "valid" {
			o.Target, o.Retry = "", false
			if o.Phase == devices.OTAPhaseOffered {
				o.Phase = devices.OTAPhaseIdle
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
	if !o.Servable(cand.Version) {
		o.Phase, o.Version, o.Build, o.Size, o.Auto = devices.OTAPhaseOffered, cand.Version, cand.Build, cand.Size, auto
		o.From, o.Error, o.StartedAt, o.FinishedAt = rep.FW, "", nil, nil
		if l := r.Last; l != nil && l.Attempt > o.Attempt {
			o.Attempt = l.Attempt
		}
		o.Attempt++
	}
	offer := &devices.OTAOffer{
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
	if auto && o.Phase == devices.OTAPhaseOffered {
		return offer, otaWaitIdleInput
	}
	return offer, ""
}

func applyOTAResult(o *devices.KnobOTA, rep devices.Checkin, now time.Time) {
	r := rep.OTA
	if o.Version == "" || r == nil || !devices.OTAActive(o.Phase) {
		return
	}
	if rep.FWBuild != "" && rep.FWBuild == o.Build {
		if r.Image == "pending_verify" {
			o.Phase = devices.OTAPhaseVerifying
			return
		}
		o.Phase, o.Error, o.FinishedAt, o.Retry = devices.OTAPhaseDone, "", &now, false
		o.Blocked = slices.DeleteFunc(o.Blocked, func(v string) bool { return v == o.Version })
		if o.Target == o.Version {
			o.Target = ""
		}
		return
	}
	if l := r.Last; l != nil && l.Attempt != 0 && l.Attempt == o.Attempt && l.Version == o.Version &&
		(l.Result == devices.OTAPhaseFailed || l.Result == devices.OTAPhaseRolledBack) {
		o.Phase, o.Error, o.FinishedAt = l.Result, l.Error, &now
		o.Block()
		return
	}
	switch {
	case r.Phase == "waiting" && (o.Phase == devices.OTAPhaseOffered || o.Phase == devices.OTAPhaseDownloading):
		o.Phase = devices.OTAPhaseInstalling
	case r.Phase == "rebooting" && (o.Phase == devices.OTAPhaseOffered || o.Phase == devices.OTAPhaseDownloading || o.Phase == devices.OTAPhaseInstalling):
		o.Phase = devices.OTAPhaseRestarting
	}
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

func (a *App) otaStatus(device string, o devices.KnobOTA, last *devices.Checkin) otaStatus {
	st := otaStatus{
		Mode:       o.EffectiveMode(),
		Target:     nonEmpty(o.Target),
		Version:    nonEmpty(o.Version),
		Phase:      o.EffectivePhase(),
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
		if o.Phase == devices.OTAPhaseDownloading && !p.doneAt.IsZero() && (last == nil || last.SeenAt.Before(p.doneAt)) {
			st.Phase = devices.OTAPhaseRestarting
		}
	}
	if last != nil {
		run := &otaRunning{FW: last.FW, Build: nonEmpty(last.FWBuild)}
		if r := last.OTA; r != nil {
			run.Slot, run.Image, run.Rollback = r.Slot, nonEmpty(r.Image), r.Rollback
		}
		st.Running = run
		if a.knobFW != nil {
			if m, ok := a.knobFW.newestAbove(last.FW, func(m firmwareMeta) bool {
				return !slices.Contains(o.Blocked, m.Version)
			}); ok {
				st.Available = &m.Version
			}
		}
	}
	return st
}
