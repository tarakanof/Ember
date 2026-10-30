package devices

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"
)

const (
	OTAModeManual       = "manual"
	OTAPhaseIdle        = "idle"
	OTAPhaseOffered     = "offered"
	OTAPhaseDownloading = "downloading"
	OTAPhaseInstalling  = "installing"
	OTAPhaseRestarting  = "restarting"
	OTAPhaseVerifying   = "verifying"
	OTAPhaseDone        = "done"
	OTAPhaseFailed      = "failed"
	OTAPhaseRolledBack  = "rolled_back"
	otaBlockedMax       = 16
)

var (
	otaErrorPattern = regexp.MustCompile(`^[a-z0-9_]{1,24}$`)
)

type KnobOTA struct {
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

func (o *KnobOTA) clone() *KnobOTA {
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

func (o KnobOTA) EffectiveMode() string {
	if o.Mode == "" {
		return OTAModeManual
	}
	return o.Mode
}

func (o KnobOTA) EffectivePhase() string {
	if o.Phase == "" {
		return OTAPhaseIdle
	}
	return o.Phase
}

func OTAActive(phase string) bool {
	switch phase {
	case OTAPhaseOffered, OTAPhaseDownloading, OTAPhaseInstalling, OTAPhaseRestarting, OTAPhaseVerifying:
		return true
	}
	return false
}

func (o KnobOTA) Servable(version string) bool {
	return o.Version == version && (o.Phase == OTAPhaseOffered || o.Phase == OTAPhaseDownloading)
}

type KnobOTAReport struct {
	Image    string       `json:"image,omitempty"`
	Last     *KnobOTALast `json:"last,omitempty"`
	Phase    string       `json:"phase,omitempty"`
	Rollback bool         `json:"rollback"`
	Slot     *int         `json:"slot,omitempty"`
}

type KnobOTALast struct {
	Attempt int    `json:"attempt,omitempty"`
	Error   string `json:"error,omitempty"`
	Result  string `json:"result"`
	Version string `json:"version,omitempty"`
}

func (r KnobOTAReport) Validate() error {
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
		case l.Result != "ok" && l.Result != OTAPhaseFailed && l.Result != OTAPhaseRolledBack:
			return errors.New("last.result must be ok, failed or rolled_back")
		case l.Error != "" && !otaErrorPattern.MatchString(l.Error):
			return errors.New("last.error must be 1..24 of a-z, 0-9, _")
		case l.Attempt < 0:
			return errors.New("last.attempt must be >= 0")
		case l.Version != "" && !SemverPattern.MatchString(l.Version):
			return errors.New("last.version must be a semantic version")
		}
	}
	return nil
}

func (r *KnobOTAReport) clone() *KnobOTAReport {
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

type OTAOffer struct {
	Attempt int    `json:"attempt"`
	Auto    bool   `json:"auto"`
	Build   string `json:"build"`
	Retry   bool   `json:"retry"`
	SHA256  string `json:"sha256"`
	Size    int    `json:"size"`
	URL     string `json:"url"`
	Version string `json:"version"`
}

func (r *Registry) OTASnapshot(id string) (KnobOTA, *Checkin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.FindKnob(id)
	if d == nil {
		return KnobOTA{}, nil, ErrNotFound
	}
	c := d.Clone()
	var o KnobOTA
	if c.OTA != nil {
		o = *c.OTA
	}
	return o, c.LastCheckin, nil
}

func (r *Registry) UpdateOTA(id string, fn func(o *KnobOTA, last *Checkin) (bool, error)) (KnobOTA, *Checkin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writableLocked(); err != nil {
		return KnobOTA{}, nil, err
	}
	d := r.state.FindKnob(id)
	if d == nil {
		return KnobOTA{}, nil, ErrNotFound
	}
	var o KnobOTA
	if d.OTA != nil {
		o = *d.OTA.clone()
	}
	before, _ := json.Marshal(o)
	last := d.Clone().LastCheckin
	bump, err := fn(&o, last)
	if err != nil {
		return KnobOTA{}, nil, err
	}
	after, _ := json.Marshal(o)
	if !bump && bytes.Equal(before, after) {
		return o, last, nil
	}
	err = r.mutateLocked(func(st *State) error {
		d := st.FindKnob(id)
		d.OTA = o.clone()
		if bump {
			st.Epoch++
		}
		return nil
	})
	return o, last, err
}

func (o KnobOTA) parked(version string) bool {
	return o.Version == version && (o.Phase == OTAPhaseFailed || o.Phase == OTAPhaseRolledBack)
}

func (o KnobOTA) holds(version string) bool {
	return (o.Target == version && !o.parked(version)) || (o.Version == version && OTAActive(o.Phase))
}

func (r *Registry) FirmwareGen() uint64 {
	gen, _ := r.OTAGens()
	return gen
}

func (r *Registry) OTAGens() (uint64, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fwGen, r.state.Epoch
}

// FirmwareGenLocked is for code the registry already calls under its lock.
func (r *Registry) FirmwareGenLocked() uint64 { return r.fwGen }

// EpochLocked is for code the registry already calls under its lock.
func (r *Registry) EpochLocked() uint64 { return r.state.Epoch }

func (r *Registry) OTATargets(version string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.state.Devices {
		if d.Kind == KindKnob && d.OTA != nil && d.OTA.holds(version) {
			return true
		}
	}
	r.fwGen++
	return false
}

func (r *Registry) HoldUnlessKept(version string) (kept bool, release func()) {
	r.mu.Lock()
	if r.keepsLocked(version) {
		r.mu.Unlock()
		return true, func() {}
	}
	return false, r.mu.Unlock
}

func (r *Registry) keepsLocked(version string) bool {
	for _, d := range r.state.Devices {
		if d.Kind != KindKnob {
			continue
		}
		if (d.OTA != nil && (d.OTA.holds(version) || d.OTA.Target == version)) ||
			(d.LastCheckin != nil && d.LastCheckin.FW == version) {
			return true
		}
	}
	r.fwGen++
	return false
}

func (r *Registry) OTARetire(versions []string, stored string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fwGen++
	unblock := func(v string) bool { return slices.Contains(versions, v) }
	gone := func(v string) bool { return v != "" && v != stored && unblock(v) }
	stale := func(o *KnobOTA) bool {
		if o == nil {
			return false
		}
		idle := !OTAActive(o.Phase)
		return slices.ContainsFunc(o.Blocked, unblock) ||
			(idle && gone(o.Target)) || (idle && o.Phase != OTAPhaseDone && gone(o.Version))
	}
	if !slices.ContainsFunc(r.state.Devices, func(d Record) bool { return d.Kind == KindKnob && stale(d.OTA) }) {
		return nil
	}
	return r.mutateLocked(func(st *State) error {
		bump := false
		for i := range st.Devices {
			o := st.Devices[i].OTA
			if st.Devices[i].Kind != KindKnob || !stale(o) {
				continue
			}
			o.Blocked = slices.DeleteFunc(o.Blocked, unblock)
			if OTAActive(o.Phase) {
				continue
			}
			if gone(o.Target) {
				o.Target, o.Retry, bump = "", false, true
			}
			if o.Phase != OTAPhaseDone && gone(o.Version) {
				*o = KnobOTA{Mode: o.Mode, Target: o.Target, Retry: o.Retry, Blocked: o.Blocked, Attempt: o.Attempt}
			}
		}
		if bump {
			st.Epoch++
		}
		return nil
	})
}

func (o *KnobOTA) Block() {
	if slices.Contains(o.Blocked, o.Version) {
		return
	}
	o.Blocked = append(o.Blocked, o.Version)
	if len(o.Blocked) > otaBlockedMax {
		o.Blocked = o.Blocked[len(o.Blocked)-otaBlockedMax:]
	}
}
