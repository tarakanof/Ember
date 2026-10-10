package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	deviceKindClock = "awtrix-ng"
	clockIDPrefix   = "clock-"
)

var clockUIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

func knobOnly(op string) error {
	return fmt.Errorf("%w: %s applies to kind %q only", errDeviceBody, op, deviceKindKnob)
}

type clockSeen struct {
	SeenAt  time.Time `json:"seen_at"`
	FW      string    `json:"fw,omitempty"`
	IP      string    `json:"ip,omitempty"`
	RSSI    *int      `json:"rssi,omitempty"`
	UptimeS *int64    `json:"uptime_s,omitempty"`
}

func (s *clockSeen) clone() *clockSeen {
	if s == nil {
		return nil
	}
	c := *s
	if s.RSSI != nil {
		v := *s.RSSI
		c.RSSI = &v
	}
	if s.UptimeS != nil {
		v := *s.UptimeS
		c.UptimeS = &v
	}
	return &c
}

func normalizeClockUID(raw string) (string, bool) {
	s := strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(strings.TrimSpace(raw)))
	return s, clockUIDPattern.MatchString(s)
}

func clockDeviceID(uid string) string {
	if len(uid) > 6 {
		uid = uid[len(uid)-6:]
	}
	return clockIDPrefix + uid
}

func (s *deviceState) findClock() *deviceRecord {
	for i := range s.Devices {
		if s.Devices[i].Kind == deviceKindClock {
			return &s.Devices[i]
		}
	}
	return nil
}

func defaultClockName(id string) string {
	return "Clock " + strings.ToUpper(strings.TrimPrefix(id, clockIDPrefix))
}

func (r *deviceRegistry) kindOf(id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.find(id)
	if d == nil {
		return "", errDeviceNotFound
	}
	return d.Kind, nil
}

func (r *deviceRegistry) hasClock() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.findClock() != nil
}

func (r *deviceRegistry) clockConfigVersion() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := r.state.findClock(); d != nil {
		return d.ConfigVersion
	}
	return 0
}

func (r *deviceRegistry) seenClock(uid string, seen *clockSeen, digest string) (deviceView, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writableLocked(); err != nil {
		return deviceView{}, false, err
	}
	if seen != nil {
		seen = seen.clone()
		seen.SeenAt = r.now().UTC()
	}
	d := r.state.findClock()
	if d != nil && d.HwID != uid && r.pendingClockUID != uid {
		r.pendingClockUID = uid
		return d.view(), false, nil
	}
	r.pendingClockUID = ""
	if d != nil && d.HwID == uid {
		if seen == nil {
			return d.view(), false, nil
		}
		d.LastSeen = seen
		r.dirty = true
		if r.now().Sub(r.persistedAt) >= deviceCheckinPersistInterval {
			if err := r.persistLocked(r.state); err != nil {
				return d.view(), false, fmt.Errorf("%w: %w", errCheckinNotStored, err)
			}
		}
		return d.view(), false, nil
	}
	var view deviceView
	err := r.mutateLocked(func(st *deviceState) error {
		if d := st.findClock(); d != nil {
			renamed := d.Name != defaultClockName(d.ID)
			d.ID = clockDeviceID(uid)
			d.HwID = uid
			if !renamed {
				d.Name = defaultClockName(d.ID)
			}
			d.LastSeen = seen
			view = d.view()
			slices.SortFunc(st.Devices, func(a, b deviceRecord) int { return strings.Compare(a.ID, b.ID) })
			st.Epoch++
			return nil
		}
		id := clockDeviceID(uid)
		d := deviceRecord{
			ID:            id,
			Kind:          deviceKindClock,
			HwID:          uid,
			Name:          defaultClockName(id),
			ConfigVersion: st.ClockVersionMark + 1,
			ConfigDigest:  digest,
			CreatedAt:     r.now().UTC(),
			LastSeen:      seen,
		}
		st.Devices = append(st.Devices, d)
		st.ClockVersionMark = d.ConfigVersion
		slices.SortFunc(st.Devices, func(a, b deviceRecord) int { return strings.Compare(a.ID, b.ID) })
		st.Epoch++
		view = d.view()
		return nil
	})
	return view, err == nil, err
}

func (r *deviceRegistry) syncClockConfig(digest string, legacyHash int) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stale := slices.ContainsFunc(r.state.Devices, func(d deviceRecord) bool {
		return d.Kind == deviceKindClock && d.ConfigDigest != digest
	})
	if !stale {
		return false, nil
	}
	bumped := false
	err := r.mutateLocked(func(st *deviceState) error {
		for i := range st.Devices {
			d := &st.Devices[i]
			if d.Kind != deviceKindClock || d.ConfigDigest == digest {
				continue
			}
			if d.ConfigDigest != "" || d.ConfigVersion != legacyHash {
				d.ConfigVersion++
				bumped = true
			}
			d.ConfigDigest = digest
			st.ClockVersionMark = max(st.ClockVersionMark, d.ConfigVersion)
		}
		if bumped {
			st.Epoch++
		}
		return nil
	})
	return bumped && err == nil, err
}

func (a *App) observeClock(rawUID string, seen *clockSeen) {
	uid, ok := normalizeClockUID(rawUID)
	if !ok || a.devices == nil || a.devices.loadError() != nil {
		return
	}
	view, created, err := a.devices.seenClock(uid, seen, clockConfigDigest(a.composeClockConfig()))
	if err != nil {
		a.logger.Warn("clock record not stored", "uid", uid, "err", err)
		return
	}
	if created {
		a.logger.Info("clock registered", "device_id", view.ID, "uid", uid)
	}
	if created || a.clockMigrate.lastError() != nil {
		a.migrateClockConfigInBackground()
	}
}

type clockSyncGate struct {
	mu     sync.Mutex
	paused int
}

func (a *App) syncClockConfigVersion() {
	if a.devices == nil {
		return
	}
	a.clockSync.mu.Lock()
	defer a.clockSync.mu.Unlock()
	if a.clockSync.paused > 0 {
		return
	}
	a.syncClockConfigLocked(a.composeClockConfig())
}

func (a *App) syncClockConfigLocked(cfg clockConfig) {
	changed, err := a.devices.syncClockConfig(clockConfigDigest(cfg), clockConfigHash(cfg))
	if err != nil {
		a.logger.Warn("clock config version not stored", "err", err)
		return
	}
	if changed {
		a.logger.Debug("clock config version changed")
	}
}

func (a *App) clockConfigSnapshot() (clockConfig, int) {
	a.clockSync.mu.Lock()
	defer a.clockSync.mu.Unlock()
	cfg := a.composeClockConfig()
	if a.devices != nil && a.clockSync.paused == 0 {
		a.syncClockConfigLocked(cfg)
	}
	return cfg, a.clockConfigVersion()
}

func (a *App) pauseClockSync() {
	a.clockSync.mu.Lock()
	a.clockSync.paused++
	a.clockSync.mu.Unlock()
}

func (a *App) resumeClockSync() {
	a.clockSync.mu.Lock()
	a.clockSync.paused--
	a.clockSync.mu.Unlock()
	a.syncClockConfigVersion()
}

func (a *App) reapplySettings() {
	a.pauseClockSync()
	a.iconHold.Add(1)
	a.settings.reapply()
	a.iconHold.Add(-1)
	a.resumeClockSync()
}
