package main

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

const (
	devicesKey                   = "devices_json"
	deviceKindKnob               = "cinder-knob"
	deviceTokenPrefix            = "ekd_"
	deviceRotationGrace          = 24 * time.Hour
	deviceCheckinPersistInterval = 10 * time.Minute
	deviceNameMaxRunes           = 64
	devicesEpochHeader           = "X-Ember-Devices-Epoch"
	deviceConfigVersion          = "X-Ember-Config-Version"
)

var (
	errDeviceNotFound   = errors.New("device not found")
	errDeviceBody       = errors.New("invalid device body")
	errNoChange         = errors.New("no change")
	errCheckinNotStored = errors.New("checkin kept in memory, store write failed")
)

type deviceCheckin struct {
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
	Wifi                *deviceWifi    `json:"wifi,omitempty"`
	Diag                *deviceDiag    `json:"diag,omitempty"`
	FWBuild             string         `json:"fw_build,omitempty"`
	OTA                 *knobOTAReport `json:"ota,omitempty"`
}

type deviceWifi struct {
	BSSID       string `json:"bssid,omitempty"`
	Channel     int    `json:"channel,omitempty"`
	Disconnects int64  `json:"disconnects"`
	LastReason  int    `json:"last_reason,omitempty"`
	RSSIMin     int    `json:"rssi_min,omitempty"`
}

var bssidPattern = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

func (w deviceWifi) validate() error {
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

type deviceRecord struct {
	ID                 string         `json:"id"`
	Kind               string         `json:"kind"`
	HwID               string         `json:"hw_id"`
	Name               string         `json:"name"`
	TokenSHA256        string         `json:"token_sha256"`
	PendingTokenSHA256 string         `json:"pending_token_sha256,omitempty"`
	RotatedAt          *time.Time     `json:"rotated_at,omitempty"`
	Config             knobSettings   `json:"config,omitzero"`
	ConfigVersion      int            `json:"config_version"`
	ConfigDigest       string         `json:"config_digest,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	LastCheckin        *deviceCheckin `json:"last_checkin,omitempty"`
	LastSeen           *clockSeen     `json:"last_seen,omitempty"`
	OTA                *knobOTA       `json:"ota,omitempty"`
	Caps               *deviceCaps    `json:"caps,omitempty"`
	CapsError          string         `json:"caps_error,omitempty"`
}

func (d deviceRecord) clone() deviceRecord {
	d.Config = d.Config.clone()
	d.Caps = d.Caps.clone()
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
		d.LastCheckin = &c
	}
	d.LastSeen = d.LastSeen.clone()
	return d
}

type deviceState struct {
	Epoch   uint64         `json:"epoch"`
	Devices []deviceRecord `json:"devices"`
}

func (s deviceState) clone() deviceState {
	out := deviceState{Epoch: s.Epoch, Devices: make([]deviceRecord, 0, len(s.Devices)+1)}
	for _, d := range s.Devices {
		out.Devices = append(out.Devices, d.clone())
	}
	return out
}

func (s *deviceState) find(id string) *deviceRecord {
	for i := range s.Devices {
		if s.Devices[i].ID == id {
			return &s.Devices[i]
		}
	}
	return nil
}

func (s *deviceState) findKnob(id string) *deviceRecord {
	if d := s.find(id); d != nil && d.Kind == deviceKindKnob {
		return d
	}
	return nil
}

type deviceView struct {
	ID              string             `json:"id"`
	Kind            string             `json:"kind"`
	HwID            string             `json:"hw_id"`
	Name            string             `json:"name"`
	CreatedAt       time.Time          `json:"created_at"`
	ConfigVersion   int                `json:"config_version"`
	RotationPending bool               `json:"rotation_pending"`
	RotatedAt       *time.Time         `json:"rotated_at"`
	LastCheckin     *deviceCheckin     `json:"last_checkin"`
	LastSeen        *clockSeen         `json:"last_seen,omitempty"`
	EffectiveCaps   *effectiveCapsView `json:"effective_caps,omitempty"`
}

func (d deviceRecord) view() deviceView {
	d = d.clone()
	v := deviceView{
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
	}
	if v.LastSeen != nil {
		v.LastSeen.SeenAt = v.LastSeen.SeenAt.UTC().Truncate(time.Second)
	}
	return v
}

type deviceRegistry struct {
	kv  func() settingsKV
	now func() time.Time

	mu           sync.Mutex
	state        deviceState
	pendingPlain map[string]string
	loadErr      error
	persistedAt  time.Time
	dirty        bool
	fwGen        uint64

	pendingClockUID string

	// Runs under mu: it must not block or call back into the registry.
	onChange func()
}

func newDeviceRegistry(kv func() settingsKV) *deviceRegistry {
	return &deviceRegistry{kv: kv, now: time.Now, pendingPlain: map[string]string{}}
}

func (r *deviceRegistry) load() error {
	kv := r.kv()
	if kv == nil {
		return nil
	}
	st, err := readDeviceState(kv)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadErr = err
	if err == nil {
		r.state = st
		r.persistedAt, r.dirty = r.now(), false
	}
	return err
}

func readDeviceState(kv settingsKV) (deviceState, error) {
	var st deviceState
	blob, ok, err := kv.GetSetting(devicesKey)
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
		if st.Devices[i].Kind == clientKind {
			return deviceState{}, fmt.Errorf("decode devices: client record %s was not moved to the client token store", st.Devices[i].ID)
		}
		if st.Devices[i].Kind == deviceKindKnob {
			st.Devices[i].Config.fillDefaults()
		}
	}
	return st, nil
}

func (r *deviceRegistry) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state, r.loadErr = deviceState{}, err
}

func (r *deviceRegistry) loadError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadErr
}

func (r *deviceRegistry) mutate(fn func(*deviceState) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mutateLocked(fn)
}

func (r *deviceRegistry) mutateLocked(fn func(*deviceState) error) error {
	if err := r.writableLocked(); err != nil {
		return err
	}
	next := r.state.clone()
	if err := fn(&next); err != nil {
		if errors.Is(err, errNoChange) {
			return nil
		}
		return err
	}
	if err := r.persistLocked(next); err != nil {
		return err
	}
	r.state = next
	if r.onChange != nil {
		r.onChange()
	}
	return nil
}

func (r *deviceRegistry) writableLocked() error {
	if r.loadErr != nil {
		return fmt.Errorf("device registry load failed, writes refused until restart: %w", r.loadErr)
	}
	return nil
}

func (r *deviceRegistry) persistLocked(st deviceState) error {
	kv := r.kv()
	if kv == nil {
		return nil
	}
	blob, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encode devices: %w", err)
	}
	if err := kv.PutSetting(devicesKey, string(blob)); err != nil {
		return fmt.Errorf("persist devices: %w", err)
	}
	r.persistedAt, r.dirty = r.now(), false
	return nil
}

func (r *deviceRegistry) flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dirty || r.loadErr != nil {
		return nil
	}
	return r.persistLocked(r.state)
}

func (r *deviceRegistry) epochValue() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Epoch
}

func (r *deviceRegistry) list() []deviceView {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]deviceView, 0, len(r.state.Devices))
	for _, d := range r.state.Devices {
		out = append(out, d.view())
	}
	return out
}

func (r *deviceRegistry) versions(id string) (uint64, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.findKnob(id)
	if d == nil {
		return 0, 0, errDeviceNotFound
	}
	return r.state.Epoch, d.ConfigVersion, nil
}

func (r *deviceRegistry) diagnostics(id string) (string, *deviceCheckin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.findKnob(id)
	if d == nil {
		return "", nil, errDeviceNotFound
	}
	c := d.clone()
	return c.Config.Diagnostics, c.LastCheckin, nil
}

func (r *deviceRegistry) settingsAndCheckin(id string) (knobSettings, *deviceCheckin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.findKnob(id)
	if d == nil {
		return knobSettings{}, nil, errDeviceNotFound
	}
	c := d.clone()
	return c.Config, c.LastCheckin, nil
}

type knobViewState struct {
	epoch   uint64
	version int
	cfg     knobSettings
	caps    *deviceCaps
}

func (r *deviceRegistry) viewState(id string) (knobViewState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.findKnob(id)
	if d == nil {
		return knobViewState{}, errDeviceNotFound
	}
	c := d.clone()
	return knobViewState{epoch: r.state.Epoch, version: c.ConfigVersion, cfg: c.Config, caps: c.Caps}, nil
}

func (r *deviceRegistry) config(id string) (knobSettings, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.findKnob(id)
	if d == nil {
		return knobSettings{}, 0, errDeviceNotFound
	}
	c := d.clone()
	return c.Config, c.ConfigVersion, nil
}

// The plaintext token is returned once and never stored.
func (r *deviceRegistry) provision(hwID, name string) (deviceView, string, bool, error) {
	token := newToken(deviceTokenPrefix)
	var view deviceView
	created := false
	err := r.mutate(func(st *deviceState) error {
		st.Epoch++
		for i := range st.Devices {
			d := &st.Devices[i]
			if d.Kind != deviceKindKnob || d.HwID != hwID {
				continue
			}
			d.TokenSHA256 = tokenHash(token)
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
		d := deviceRecord{
			ID:            deviceID(st, hwID),
			Kind:          deviceKindKnob,
			HwID:          hwID,
			Name:          name,
			TokenSHA256:   tokenHash(token),
			Config:        defaultKnobSettings(),
			ConfigVersion: 1,
			CreatedAt:     r.now().UTC(),
		}
		st.Devices = append(st.Devices, d)
		slices.SortFunc(st.Devices, func(a, b deviceRecord) int { return strings.Compare(a.ID, b.ID) })
		view = d.view()
		return nil
	})
	return view, token, created, err
}

func deviceID(st *deviceState, hwID string) string {
	id := "knob-" + hwID[len(hwID)-6:]
	if st.find(id) != nil {
		return "knob-" + hwID
	}
	return id
}

func (r *deviceRegistry) rename(id, name string) (deviceView, error) {
	var view deviceView
	err := r.mutate(func(st *deviceState) error {
		d := st.find(id)
		if d == nil {
			return errDeviceNotFound
		}
		d.Name = name
		view = d.view()
		return nil
	})
	return view, err
}

func (r *deviceRegistry) rotate(id string) (deviceView, error) {
	var view deviceView
	err := r.mutate(func(st *deviceState) error {
		d := st.find(id)
		if d == nil {
			return errDeviceNotFound
		}
		if d.Kind != deviceKindKnob {
			return knobOnly("rotate")
		}
		now := r.now().UTC()
		d.RotatedAt = &now
		d.PendingTokenSHA256 = ""
		st.Epoch++
		view = d.view()
		return nil
	})
	return view, err
}

func (r *deviceRegistry) remove(id string) error {
	return r.mutate(func(st *deviceState) error {
		i := slices.IndexFunc(st.Devices, func(d deviceRecord) bool { return d.ID == id })
		if i < 0 {
			return errDeviceNotFound
		}
		st.Devices = slices.Delete(st.Devices, i, i+1)
		delete(r.pendingPlain, id)
		st.Epoch++
		return nil
	})
}

func (r *deviceRegistry) putConfig(id string, patch []byte) (knobSettings, int, bool, error) {
	var out knobSettings
	var version int
	changed := false
	err := r.mutate(func(st *deviceState) error {
		d := st.findKnob(id)
		if d == nil {
			return errDeviceNotFound
		}
		merged, err := mergeKnobSettings(d.clone().Config, patch)
		if err != nil {
			return err
		}
		if err := merged.validate(); err != nil {
			return fmt.Errorf("%w: %w", errSettingBody, err)
		}
		before, _ := json.Marshal(d.Config)
		after, _ := json.Marshal(merged)
		out, version = merged, d.ConfigVersion
		if string(before) == string(after) {
			return errNoChange
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

func (r *deviceRegistry) authenticate(token string) (string, bool, error) {
	if !strings.HasPrefix(token, deviceTokenPrefix) {
		return "", false, nil
	}
	h := []byte(tokenHash(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writableLocked(); err != nil {
		return "", false, err
	}
	now := r.now()
	match, pending := -1, false
	for i, d := range r.state.Devices {
		if d.Kind != deviceKindKnob {
			continue
		}
		cur := subtle.ConstantTimeCompare(h, []byte(d.TokenSHA256)) == 1
		if cur && d.RotatedAt != nil && now.Sub(*d.RotatedAt) > deviceRotationGrace {
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
	err := r.mutateLocked(func(st *deviceState) error {
		d := st.find(id)
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

type checkinResult struct {
	ConfigVersion int           `json:"config_version"`
	Config        *knobSettings `json:"config,omitempty"`
	NewToken      string        `json:"new_token,omitempty"`
	// server Unix seconds.
	DiagLiveUntil  *int64    `json:"diag_live_until,omitempty"`
	CoredumpWanted string    `json:"coredump_wanted,omitempty"`
	CoredumpAck    string    `json:"coredump_ack,omitempty"`
	OTA            *otaOffer `json:"ota,omitempty"`
	CapsAck        bool      `json:"caps_ack,omitempty"`
	newCrash       *deviceCrash
}

func (r *deviceRegistry) checkin(id string, report deviceCheckin, caps *deviceCaps, capsErr string) (checkinResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writableLocked(); err != nil {
		return checkinResult{}, err
	}
	if d := r.state.find(id); d != nil && d.Kind != deviceKindKnob {
		return checkinResult{}, knobOnly("checkin")
	}
	d := r.state.findKnob(id)
	if d == nil {
		return checkinResult{}, errDeviceNotFound
	}
	report.SeenAt = r.now().UTC()
	res := checkinResult{ConfigVersion: d.ConfigVersion, CapsAck: caps != nil}
	capsChanged := !d.Caps.equal(caps) || d.CapsError != capsErr
	var prevDiag *deviceDiag
	if d.LastCheckin != nil {
		prevDiag = d.LastCheckin.Diag
	}
	if report.Diag != nil {
		report.Diag.carryFrom(prevDiag)
	}
	res.newCrash = newDiagCrash(prevDiag, report.Diag)
	if report.AppliedVersion != d.ConfigVersion {
		c := d.clone().Config
		res.Config = &c
	}
	if d.RotatedAt != nil {
		res.NewToken = r.pendingPlain[id]
		if res.NewToken == "" || tokenHash(res.NewToken) != d.PendingTokenSHA256 {
			res.NewToken = newToken(deviceTokenPrefix)
			err := r.mutateLocked(func(st *deviceState) error {
				d := st.find(id)
				d.LastCheckin = &report
				if capsChanged {
					d.Caps, d.CapsError = caps.clone(), capsErr
				}
				d.PendingTokenSHA256 = tokenHash(res.NewToken)
				return nil
			})
			if err != nil {
				return checkinResult{}, err
			}
			r.pendingPlain[id] = res.NewToken
			return res, nil
		}
	}
	d.LastCheckin = &report
	if capsChanged {
		d.Caps, d.CapsError = caps.clone(), capsErr
	}
	r.dirty = true
	if capsChanged && r.onChange != nil {
		r.onChange()
	}
	if capsChanged || r.now().Sub(r.persistedAt) >= deviceCheckinPersistInterval {
		if err := r.persistLocked(r.state); err != nil {
			return res, fmt.Errorf("%w: %w", errCheckinNotStored, err)
		}
	}
	return res, nil
}

func mergeKnobSettings(cur knobSettings, patch []byte) (knobSettings, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(patch, &keys); err != nil || keys == nil {
		return cur, errSettingNotObject
	}
	for k := range keys {
		if strings.EqualFold(k, "pages") {
			cur.Pages = nil
		}
	}
	dec := json.NewDecoder(bytes.NewReader(patch))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cur); err != nil {
		return cur, fmt.Errorf("%w: %w", errSettingBody, err)
	}
	cur.Bot.fillDefaults()
	cur.Display.fillDefaults()
	cur.Quiet.fillDefaults()
	cur.addKnownPages()
	return cur, nil
}

func newToken(prefix string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func normalizeHwID(raw string) (string, error) {
	s := strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(strings.TrimSpace(raw)))
	if len(s) != 12 {
		return "", fmt.Errorf("%w: hw_id must be 12 hex digits", errDeviceBody)
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", fmt.Errorf("%w: hw_id must be 12 hex digits", errDeviceBody)
	}
	return s, nil
}

func normalizeDeviceName(raw string, required bool) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" && required {
		return "", fmt.Errorf("%w: name is required", errDeviceBody)
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > deviceNameMaxRunes {
		return "", fmt.Errorf("%w: name must be valid UTF-8 of at most %d characters", errDeviceBody, deviceNameMaxRunes)
	}
	return s, nil
}
