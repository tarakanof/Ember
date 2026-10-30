package devices

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	KindClock     = "awtrix-ng"
	ClockIDPrefix = "clock-"
)

func knobOnly(op string) error {
	return fmt.Errorf("%w: %s applies to kind %q only", ErrBody, op, KindKnob)
}

type ClockSeen struct {
	SeenAt  time.Time `json:"seen_at"`
	FW      string    `json:"fw,omitempty"`
	IP      string    `json:"ip,omitempty"`
	RSSI    *int      `json:"rssi,omitempty"`
	UptimeS *int64    `json:"uptime_s,omitempty"`
}

func (s *ClockSeen) clone() *ClockSeen {
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

func clockDeviceID(uid string) string {
	if len(uid) > 6 {
		uid = uid[len(uid)-6:]
	}
	return ClockIDPrefix + uid
}

func (s *State) FindClock() *Record {
	for i := range s.Devices {
		if s.Devices[i].Kind == KindClock {
			return &s.Devices[i]
		}
	}
	return nil
}

func defaultClockName(id string) string {
	return "Clock " + strings.ToUpper(strings.TrimPrefix(id, ClockIDPrefix))
}

func (r *Registry) KindOf(id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.Find(id)
	if d == nil {
		return "", ErrNotFound
	}
	return d.Kind, nil
}

func (r *Registry) HasClock() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.FindClock() != nil
}

func (r *Registry) ClockConfigVersion() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := r.state.FindClock(); d != nil {
		return d.ConfigVersion
	}
	return 0
}

func (r *Registry) SeenClock(uid string, seen *ClockSeen, digest string) (View, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writableLocked(); err != nil {
		return View{}, false, err
	}
	if seen != nil {
		seen = seen.clone()
		seen.SeenAt = r.Now().UTC()
	}
	d := r.state.FindClock()
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
		if r.Now().Sub(r.persistedAt) >= CheckinPersistInterval {
			if err := r.persistLocked(r.state); err != nil {
				return d.view(), false, fmt.Errorf("%w: %w", ErrCheckinNotStored, err)
			}
		}
		return d.view(), false, nil
	}
	var view View
	err := r.mutateLocked(func(st *State) error {
		if d := st.FindClock(); d != nil {
			renamed := d.Name != defaultClockName(d.ID)
			d.ID = clockDeviceID(uid)
			d.HwID = uid
			if !renamed {
				d.Name = defaultClockName(d.ID)
			}
			d.LastSeen = seen
			view = d.view()
			slices.SortFunc(st.Devices, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
			st.Epoch++
			return nil
		}
		id := clockDeviceID(uid)
		d := Record{
			ID:            id,
			Kind:          KindClock,
			HwID:          uid,
			Name:          defaultClockName(id),
			ConfigVersion: st.ClockVersionMark + 1,
			ConfigDigest:  digest,
			CreatedAt:     r.Now().UTC(),
			LastSeen:      seen,
		}
		st.Devices = append(st.Devices, d)
		st.ClockVersionMark = d.ConfigVersion
		slices.SortFunc(st.Devices, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
		st.Epoch++
		view = d.view()
		return nil
	})
	return view, err == nil, err
}

func (r *Registry) SyncClockConfig(digest string, legacyHash int) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stale := slices.ContainsFunc(r.state.Devices, func(d Record) bool {
		return d.Kind == KindClock && d.ConfigDigest != digest
	})
	if !stale {
		return false, nil
	}
	bumped := false
	err := r.mutateLocked(func(st *State) error {
		for i := range st.Devices {
			d := &st.Devices[i]
			if d.Kind != KindClock || d.ConfigDigest == digest {
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
