package devices

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type KV interface {
	GetSetting(key string) (value string, ok bool, err error)
	PutSetting(key, value string) error
}

const (
	RebootUptimeSlack  = 10 * time.Second
	staleCheckinWindow = time.Minute
)

const (
	StateKey               = "devices_json"
	KindKnob               = "cinder-knob"
	TokenPrefix            = "ekd_"
	RotationGrace          = 24 * time.Hour
	CheckinPersistInterval = 10 * time.Minute
	deviceNameMaxRunes     = 64
)

var (
	ErrNotFound         = errors.New("device not found")
	ErrBody             = errors.New("invalid device body")
	ErrNoChange         = errors.New("no change")
	ErrCheckinNotStored = errors.New("checkin kept in memory, store write failed")
)

type Checkin struct {
	SeenAt              time.Time      `json:"seen_at"`
	FW                  string         `json:"fw"`
	IP                  string         `json:"ip"`
	RSSI                int            `json:"rssi"`
	HeapInternalFree    int            `json:"heap_internal_free"`
	HeapInternalLargest int            `json:"heap_internal_largest"`
	UptimeS             int64          `json:"uptime_s"`
	AppliedVersion      int            `json:"applied_version"`
	LinkMHz             int            `json:"link_mhz,omitempty"`
	LinkFallback        bool           `json:"link_fallback,omitempty"`
	Wifi                *Wifi          `json:"wifi,omitempty"`
	Diag                *Diag          `json:"diag,omitempty"`
	FWBuild             string         `json:"fw_build,omitempty"`
	OTA                 *KnobOTAReport `json:"ota,omitempty"`
	Room                *Room          `json:"room,omitempty"`
}

type Wifi struct {
	BSSID       string `json:"bssid,omitempty"`
	Channel     int    `json:"channel,omitempty"`
	Disconnects int64  `json:"disconnects"`
	LastReason  int    `json:"last_reason,omitempty"`
	RSSIMin     int    `json:"rssi_min,omitempty"`
}

var bssidPattern = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

func (w Wifi) Validate() error {
	switch {
	case w.BSSID != "" && !bssidPattern.MatchString(w.BSSID):
		return errors.New("bssid must be lower-case aa:bb:cc:dd:ee:ff")
	case w.Channel < 0 || w.Channel > 14:
		return errors.New("channel must be 0..14")
	case w.Disconnects < 0:
		return errors.New("disconnects must be >= 0")
	case w.LastReason < 0 || w.LastReason > 255:
		return errors.New("last_reason must be 0..255")
	case w.RSSIMin < -127 || w.RSSIMin > 0:
		return errors.New("rssi_min must be -127..0")
	}
	return nil
}

type Record struct {
	ID                 string       `json:"id"`
	Kind               string       `json:"kind"`
	HwID               string       `json:"hw_id"`
	Name               string       `json:"name"`
	TokenSHA256        string       `json:"token_sha256"`
	PendingTokenSHA256 string       `json:"pending_token_sha256,omitempty"`
	RotatedAt          *time.Time   `json:"rotated_at,omitempty"`
	Config             KnobSettings `json:"config,omitzero"`
	ConfigVersion      int          `json:"config_version"`
	ConfigDigest       string       `json:"config_digest,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	LastCheckin        *Checkin     `json:"last_checkin,omitempty"`
	LastSeen           *ClockSeen   `json:"last_seen,omitempty"`
	OTA                *KnobOTA     `json:"ota,omitempty"`
	Caps               *Caps        `json:"caps,omitempty"`
	CapsError          string       `json:"caps_error,omitempty"`
}

func (d Record) Clone() Record {
	d.Config = d.Config.clone()
	d.Caps = d.Caps.Clone()
	d.OTA = d.OTA.clone()
	if d.RotatedAt != nil {
		t := *d.RotatedAt
		d.RotatedAt = &t
	}
	if d.LastCheckin != nil {
		c := *d.LastCheckin
		if c.Wifi != nil {
			w := *c.Wifi
			c.Wifi = &w
		}
		c.Diag = c.Diag.clone()
		c.OTA = c.OTA.clone()
		c.Room = c.Room.clone()
		d.LastCheckin = &c
	}
	d.LastSeen = d.LastSeen.clone()
	return d
}

type State struct {
	Epoch            uint64   `json:"epoch"`
	Devices          []Record `json:"devices"`
	ClockVersionMark int      `json:"clock_version_mark,omitempty"`
}

func (s State) clone() State {
	out := State{Epoch: s.Epoch, ClockVersionMark: s.ClockVersionMark, Devices: make([]Record, 0, len(s.Devices)+1)}
	for _, d := range s.Devices {
		out.Devices = append(out.Devices, d.Clone())
	}
	return out
}

func (s *State) Find(id string) *Record {
	for i := range s.Devices {
		if s.Devices[i].ID == id {
			return &s.Devices[i]
		}
	}
	return nil
}

func (s *State) FindKnob(id string) *Record {
	if d := s.Find(id); d != nil && d.Kind == KindKnob {
		return d
	}
	return nil
}

type View struct {
	ID              string             `json:"id"`
	Kind            string             `json:"kind"`
	HwID            string             `json:"hw_id"`
	Name            string             `json:"name"`
	CreatedAt       time.Time          `json:"created_at"`
	ConfigVersion   int                `json:"config_version"`
	RotationPending bool               `json:"rotation_pending"`
	RotatedAt       *time.Time         `json:"rotated_at"`
	LastCheckin     *Checkin           `json:"last_checkin"`
	LastSeen        *ClockSeen         `json:"last_seen,omitempty"`
	EffectiveCaps   *effectiveCapsView `json:"effective_caps,omitempty"`
	Shown           *KnobShown         `json:"shown,omitempty"`
}

func (d Record) view() View {
	d = d.Clone()
	v := View{
		ID:              d.ID,
		Kind:            d.Kind,
		HwID:            d.HwID,
		Name:            d.Name,
		CreatedAt:       d.CreatedAt.UTC().Truncate(time.Second),
		ConfigVersion:   d.ConfigVersion,
		RotationPending: d.RotatedAt != nil,
		LastCheckin:     d.LastCheckin,
		LastSeen:        d.LastSeen,
		EffectiveCaps:   d.effectiveCapsView(),
	}
	if d.RotatedAt != nil {
		t := d.RotatedAt.UTC().Truncate(time.Second)
		v.RotatedAt = &t
	}
	if v.LastCheckin != nil {
		v.LastCheckin.SeenAt = v.LastCheckin.SeenAt.UTC().Truncate(time.Second)
		if room := v.LastCheckin.Room; room != nil {
			room.ReceivedAt = room.ReceivedAt.UTC().Truncate(time.Second)
		}
	}
	if v.LastSeen != nil {
		v.LastSeen.SeenAt = v.LastSeen.SeenAt.UTC().Truncate(time.Second)
	}
	return v
}

type Registry struct {
	KV  func() KV
	Now func() time.Time

	mu           sync.Mutex
	state        State
	pendingPlain map[string]string
	loadErr      error
	persistedAt  time.Time
	dirty        bool
	fwGen        uint64

	pendingClockUID string

	// Runs under mu: it must not block or call back into the registry.
	OnChange func()
}

func New(kv func() KV) *Registry {
	return &Registry{KV: kv, Now: time.Now, pendingPlain: map[string]string{}}
}

func (r *Registry) Load() error {
	kv := r.KV()
	if kv == nil {
		return nil
	}
	st, err := readDeviceState(kv)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadErr = err
	if err == nil {
		r.state = st
		r.persistedAt, r.dirty = r.Now(), false
	}
	return err
}

func readDeviceState(kv KV) (State, error) {
	var st State
	blob, ok, err := kv.GetSetting(StateKey)
	if err != nil {
		return st, fmt.Errorf("read devices: %w", err)
	}
	if !ok {
		return st, nil
	}
	if err := json.Unmarshal([]byte(blob), &st); err != nil {
		return st, fmt.Errorf("decode devices: %w", err)
	}
	for i := range st.Devices {
		if st.Devices[i].Kind == KindClient {
			return State{}, fmt.Errorf("decode devices: client record %s was not moved to the client token store", st.Devices[i].ID)
		}
		if st.Devices[i].Kind == KindKnob {
			st.Devices[i].Config.FillDefaults()
		}
	}
	return st, nil
}

func (r *Registry) Fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state, r.loadErr = State{}, err
}

func (r *Registry) LoadError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadErr
}

func (r *Registry) mutate(fn func(*State) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mutateLocked(fn)
}

func (r *Registry) mutateLocked(fn func(*State) error) error {
	if err := r.writableLocked(); err != nil {
		return err
	}
	next := r.state.clone()
	if err := fn(&next); err != nil {
		if errors.Is(err, ErrNoChange) {
			return nil
		}
		return err
	}
	if err := r.persistLocked(next); err != nil {
		return err
	}
	r.state = next
	if r.OnChange != nil {
		r.OnChange()
	}
	return nil
}

func (r *Registry) writableLocked() error {
	if r.loadErr != nil {
		return fmt.Errorf("device registry load failed, writes refused until restart: %w", r.loadErr)
	}
	return nil
}

func (r *Registry) persistLocked(st State) error {
	kv := r.KV()
	if kv == nil {
		return nil
	}
	blob, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encode devices: %w", err)
	}
	if err := kv.PutSetting(StateKey, string(blob)); err != nil {
		return fmt.Errorf("persist devices: %w", err)
	}
	r.persistedAt, r.dirty = r.Now(), false
	return nil
}

func (r *Registry) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dirty || r.loadErr != nil {
		return nil
	}
	return r.persistLocked(r.state)
}

func (r *Registry) Epoch() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Epoch
}

func (r *Registry) LockFree() bool {
	if !r.mu.TryLock() {
		return false
	}
	r.mu.Unlock()
	return true
}

func (r *Registry) WithState(fn func(*State)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.state)
}

func (r *Registry) List() []View {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]View, 0, len(r.state.Devices))
	for _, d := range r.state.Devices {
		out = append(out, d.view())
	}
	return out
}

func (r *Registry) Versions(id string) (uint64, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.FindKnob(id)
	if d == nil {
		return 0, 0, ErrNotFound
	}
	return r.state.Epoch, d.ConfigVersion, nil
}

func (r *Registry) Diagnostics(id string) (string, *Checkin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.FindKnob(id)
	if d == nil {
		return "", nil, ErrNotFound
	}
	c := d.Clone()
	return c.Config.Diagnostics, c.LastCheckin, nil
}

func (r *Registry) SettingsAndCheckin(id string) (KnobSettings, *Checkin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.FindKnob(id)
	if d == nil {
		return KnobSettings{}, nil, ErrNotFound
	}
	c := d.Clone()
	return c.Config, c.LastCheckin, nil
}

type KnobViewState struct {
	Epoch   uint64
	Version int
	Cfg     KnobSettings
	Caps    *Caps
}

func (r *Registry) ViewState(id string) (KnobViewState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.FindKnob(id)
	if d == nil {
		return KnobViewState{}, ErrNotFound
	}
	c := d.Clone()
	return KnobViewState{Epoch: r.state.Epoch, Version: c.ConfigVersion, Cfg: c.Config, Caps: c.Caps}, nil
}

func (r *Registry) LockedKnob(id string, fn func(caps *Caps)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := r.state.FindKnob(id); d != nil {
		fn(d.Caps)
	}
}

func (r *Registry) EachKnobCaps(fn func(id string, caps *Caps)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.state.Devices {
		if d := &r.state.Devices[i]; d.Kind == KindKnob {
			fn(d.ID, d.Caps)
		}
	}
}

func (r *Registry) Config(id string) (KnobSettings, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.FindKnob(id)
	if d == nil {
		return KnobSettings{}, 0, ErrNotFound
	}
	c := d.Clone()
	return c.Config, c.ConfigVersion, nil
}

// The plaintext token is returned once and never stored.
func (r *Registry) Provision(hwID, name string) (View, string, bool, error) {
	token := NewToken(TokenPrefix)
	var view View
	created := false
	err := r.mutate(func(st *State) error {
		st.Epoch++
		for i := range st.Devices {
			d := &st.Devices[i]
			if d.Kind != KindKnob || d.HwID != hwID {
				continue
			}
			d.TokenSHA256 = TokenHash(token)
			d.PendingTokenSHA256 = ""
			d.RotatedAt = nil
			delete(r.pendingPlain, d.ID)
			if name != "" {
				d.Name = name
			}
			view = d.view()
			return nil
		}
		created = true
		if name == "" {
			name = "Knob " + strings.ToUpper(hwID[len(hwID)-6:])
		}
		d := Record{
			ID:            KnobID(st, hwID),
			Kind:          KindKnob,
			HwID:          hwID,
			Name:          name,
			TokenSHA256:   TokenHash(token),
			Config:        DefaultKnobSettings(),
			ConfigVersion: 1,
			CreatedAt:     r.Now().UTC(),
		}
		st.Devices = append(st.Devices, d)
		slices.SortFunc(st.Devices, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
		view = d.view()
		return nil
	})
	return view, token, created, err
}

func KnobID(st *State, hwID string) string {
	id := "knob-" + hwID[len(hwID)-6:]
	if st.Find(id) != nil {
		return "knob-" + hwID
	}
	return id
}

func (r *Registry) Rename(id, name string) (View, error) {
	var view View
	err := r.mutate(func(st *State) error {
		d := st.Find(id)
		if d == nil {
			return ErrNotFound
		}
		d.Name = name
		view = d.view()
		return nil
	})
	return view, err
}

func (r *Registry) Rotate(id string) (View, error) {
	var view View
	err := r.mutate(func(st *State) error {
		d := st.Find(id)
		if d == nil {
			return ErrNotFound
		}
		if d.Kind != KindKnob {
			return knobOnly("rotate")
		}
		now := r.Now().UTC()
		d.RotatedAt = &now
		d.PendingTokenSHA256 = ""
		st.Epoch++
		view = d.view()
		return nil
	})
	return view, err
}

func (r *Registry) Remove(id string) error {
	return r.mutate(func(st *State) error {
		i := slices.IndexFunc(st.Devices, func(d Record) bool { return d.ID == id })
		if i < 0 {
			return ErrNotFound
		}
		if d := st.Devices[i]; d.Kind == KindClock {
			st.ClockVersionMark = max(st.ClockVersionMark, d.ConfigVersion)
		}
		st.Devices = slices.Delete(st.Devices, i, i+1)
		delete(r.pendingPlain, id)
		st.Epoch++
		return nil
	})
}

func (r *Registry) PutConfig(id string, patch []byte) (KnobSettings, int, bool, error) {
	var out KnobSettings
	var version int
	changed := false
	err := r.mutate(func(st *State) error {
		d := st.FindKnob(id)
		if d == nil {
			return ErrNotFound
		}
		merged, err := mergeKnobSettings(d.Clone().Config, patch)
		if err != nil {
			return err
		}
		if err := merged.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrSettingBody, err)
		}
		before, _ := json.Marshal(d.Config)
		after, _ := json.Marshal(merged)
		out, version = merged, d.ConfigVersion
		if string(before) == string(after) {
			return ErrNoChange
		}
		if err := checkConfigAgainstCaps(d.Caps, d.Config, merged, len(before), len(after)); err != nil {
			return err
		}
		changed = true
		d.Config = merged
		d.ConfigVersion++
		version = d.ConfigVersion
		st.Epoch++
		return nil
	})
	return out, version, changed, err
}

func (r *Registry) Authenticate(token string) (string, bool, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return "", false, nil
	}
	h := []byte(TokenHash(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writableLocked(); err != nil {
		return "", false, err
	}
	now := r.Now()
	match, pending := -1, false
	for i, d := range r.state.Devices {
		if d.Kind != KindKnob {
			continue
		}
		cur := subtle.ConstantTimeCompare(h, []byte(d.TokenSHA256)) == 1
		if cur && d.RotatedAt != nil && now.Sub(*d.RotatedAt) > RotationGrace {
			cur = false
		}
		pend := d.PendingTokenSHA256 != "" && subtle.ConstantTimeCompare(h, []byte(d.PendingTokenSHA256)) == 1
		if cur || pend {
			match, pending = i, pend
		}
	}
	if match < 0 {
		return "", false, nil
	}
	id := r.state.Devices[match].ID
	if !pending {
		return id, true, nil
	}
	err := r.mutateLocked(func(st *State) error {
		d := st.Find(id)
		d.TokenSHA256 = d.PendingTokenSHA256
		d.PendingTokenSHA256 = ""
		d.RotatedAt = nil
		delete(r.pendingPlain, d.ID)
		st.Epoch++
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

type CheckinResult struct {
	ConfigVersion int           `json:"config_version"`
	Config        *KnobSettings `json:"config,omitempty"`
	NewToken      string        `json:"new_token,omitempty"`
	// server Unix seconds.
	DiagLiveUntil  *int64    `json:"diag_live_until,omitempty"`
	CoredumpWanted string    `json:"coredump_wanted,omitempty"`
	CoredumpAck    string    `json:"coredump_ack,omitempty"`
	OTA            *OTAOffer `json:"ota,omitempty"`
	CapsAck        bool      `json:"caps_ack,omitempty"`
	NewCrash       *Crash    `json:"-"`
	BootedAt       time.Time `json:"-"`
	Stale          bool      `json:"-"`
}

func (r *Registry) Checkin(id string, report Checkin, caps *Caps, capsErr string) (CheckinResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writableLocked(); err != nil {
		return CheckinResult{}, err
	}
	if d := r.state.Find(id); d != nil && d.Kind != KindKnob {
		return CheckinResult{}, knobOnly("checkin")
	}
	d := r.state.FindKnob(id)
	if d == nil {
		return CheckinResult{}, ErrNotFound
	}
	report.SeenAt = r.Now().UTC()
	res := CheckinResult{ConfigVersion: d.ConfigVersion, CapsAck: caps != nil}
	stale := knobCheckinStale(d.LastCheckin, report)
	res.Stale = stale
	capsChanged := !stale && (!d.Caps.equal(caps) || d.CapsError != capsErr)
	var prevDiag *Diag
	if d.LastCheckin != nil {
		prevDiag = d.LastCheckin.Diag
	}
	if report.Diag != nil {
		report.Diag.carryFrom(prevDiag)
	}
	if !stale {
		res.NewCrash = newDiagCrash(prevDiag, report.Diag)
	}
	if knobRebooted(d.LastCheckin, report) {
		res.BootedAt = report.bootedAt()
	}
	if report.Room != nil {
		report.Room.ReceivedAt = report.SeenAt
	} else if d.LastCheckin != nil {
		report.Room = d.LastCheckin.Room
	}
	last := &report
	if stale {
		last = d.LastCheckin
	}
	if report.AppliedVersion != d.ConfigVersion {
		c := d.Clone().Config
		res.Config = &c
	}
	if d.RotatedAt != nil {
		res.NewToken = r.pendingPlain[id]
		if res.NewToken == "" || TokenHash(res.NewToken) != d.PendingTokenSHA256 {
			res.NewToken = NewToken(TokenPrefix)
			err := r.mutateLocked(func(st *State) error {
				d := st.Find(id)
				d.LastCheckin = last
				if capsChanged {
					d.Caps, d.CapsError = caps.Clone(), capsErr
				}
				d.PendingTokenSHA256 = TokenHash(res.NewToken)
				return nil
			})
			if err != nil {
				return CheckinResult{}, err
			}
			r.pendingPlain[id] = res.NewToken
			return res, nil
		}
	}
	d.LastCheckin = last
	if capsChanged {
		d.Caps, d.CapsError = caps.Clone(), capsErr
	}
	r.dirty = true
	if capsChanged && r.OnChange != nil {
		r.OnChange()
	}
	if capsChanged || r.Now().Sub(r.persistedAt) >= CheckinPersistInterval {
		if err := r.persistLocked(r.state); err != nil {
			return res, fmt.Errorf("%w: %w", ErrCheckinNotStored, err)
		}
	}
	return res, nil
}

func knobRebooted(prev *Checkin, cur Checkin) bool {
	if prev == nil {
		return false
	}
	pb, cb := prev.boots(), cur.boots()
	if pb > 0 && cb > 0 && cb >= pb {
		return cb > pb
	}
	if prev.UptimeS <= 0 || cur.UptimeS <= 0 || cur.UptimeS >= prev.UptimeS {
		return false
	}
	return time.Duration(cur.UptimeS)*time.Second <= cur.SeenAt.Sub(prev.SeenAt)+RebootUptimeSlack
}

func knobCheckinStale(prev *Checkin, cur Checkin) bool {
	if prev == nil || knobRebooted(prev, cur) || cur.SeenAt.Sub(prev.SeenAt) > staleCheckinWindow {
		return false
	}
	known := prev.UptimeS > 0 && cur.UptimeS > 0
	pb, cb := prev.boots(), cur.boots()
	bootsKnown := pb > 0 && cb > 0
	if bootsKnown && cb != pb {
		return cb < pb && known
	}
	if !known {
		return false
	}
	grown := time.Duration(cur.UptimeS-prev.UptimeS) * time.Second
	return grown < 0 || (!bootsKnown && grown > cur.SeenAt.Sub(prev.SeenAt)+RebootUptimeSlack)
}

func (c Checkin) boots() int64 {
	if c.Diag == nil {
		return 0
	}
	return c.Diag.Boots
}

func (c Checkin) bootedAt() time.Time {
	if c.UptimeS <= 0 {
		return time.Time{}
	}
	return c.SeenAt.Add(-time.Duration(c.UptimeS) * time.Second)
}

func mergeKnobSettings(cur KnobSettings, patch []byte) (KnobSettings, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(patch, &keys); err != nil || keys == nil {
		return cur, ErrSettingNotObject
	}
	for k := range keys {
		if strings.EqualFold(k, "pages") {
			cur.Pages = nil
		}
	}
	dec := json.NewDecoder(bytes.NewReader(patch))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cur); err != nil {
		return cur, fmt.Errorf("%w: %w", ErrSettingBody, err)
	}
	cur.normalizeColors()
	cur.pruneEmptyV2()
	cur.fillFlagDefaults()
	cur.addKnownPages()
	return cur, nil
}

func NewToken(prefix string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func NormalizeHwID(raw string) (string, error) {
	s := strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(strings.TrimSpace(raw)))
	if len(s) != 12 {
		return "", fmt.Errorf("%w: hw_id must be 12 hex digits", ErrBody)
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", fmt.Errorf("%w: hw_id must be 12 hex digits", ErrBody)
	}
	return s, nil
}

func NormalizeName(raw string, required bool) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" && required {
		return "", fmt.Errorf("%w: name is required", ErrBody)
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > deviceNameMaxRunes {
		return "", fmt.Errorf("%w: name must be valid UTF-8 of at most %d characters", ErrBody, deviceNameMaxRunes)
	}
	return s, nil
}

type KnobShown struct {
	Page string    `json:"page"`
	Face *string   `json:"face"`
	At   time.Time `json:"at"`
}
