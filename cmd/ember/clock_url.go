package main

import "encoding/json"

// The clock URL has three tiers, all held in one Config value so a reader
// gets a URL and its source from the same load:
//
//   - AWTRIX.HTTPBaseURL: the config.json baseline. Only config loading and
//     /admin/reload write it; nothing at runtime overwrites it.
//   - AWTRIX.clockOverride: the menu's pick (PUT /v1/device/config). A
//     settings-overlay registration (clockSettingSpec) validates, persists
//     and re-applies it like every other menu setting.
//   - AWTRIX.clockDiscovered: a clock that rediscoverClock swapped in because
//     the effective URL stopped answering. In memory only: never persisted,
//     never written over the override.
//
// clockURL is the one place that turns those tiers into the effective URL.
// Everything that dials, reports or derives from the clock URL asks it.

// clockSource names the tier the effective clock URL came from. The strings
// are wire values (GET /v1/device/config "source", doctor).
type clockSource = string

const (
	clockSourceStore      clockSource = "store"
	clockSourceConfig     clockSource = "config"
	clockSourceDiscovered clockSource = "discovered"
	clockSourceNone       clockSource = "none"
)

// clockURL resolves the effective clock URL and where it came from.
//
// The documented order is store override > reachable config.json baseline >
// mDNS auto-pick. Reachability is what rediscoverClock tests: it sets the
// discovered tier only after the effective URL failed its probes, so a
// discovered URL sits above the tiers it replaced. The tier is cleared when
// the menu pins a URL (a PUT naming base_url) or a reload changes the file
// URL, which hands precedence back to override > baseline.
func (c *Config) clockURL() (string, clockSource) {
	a := c.AWTRIX
	switch {
	case a.clockDiscovered != "":
		return a.clockDiscovered, clockSourceDiscovered
	case a.clockOverride != "":
		return a.clockOverride, clockSourceStore
	case a.HTTPBaseURL != "":
		return a.HTTPBaseURL, clockSourceConfig
	}
	return "", clockSourceNone
}

// effectiveClockURL is clockURL without the source.
func (c *Config) effectiveClockURL() string {
	u, _ := c.clockURL()
	return u
}

// carryClockURL moves the runtime tiers from the running config onto next, a
// freshly loaded file config, inside /admin/reload's critical section. The
// override always survives (the overlay's reapply then re-lays the stored
// one). A discovery swap survives only while the file URL is unchanged: an
// edited file URL is the operator pinning a clock, and a dead one is found
// again by the next watch tick.
func carryClockURL(old Config, next *Config) {
	next.AWTRIX.clockOverride = old.AWTRIX.clockOverride
	if next.AWTRIX.HTTPBaseURL == old.AWTRIX.HTTPBaseURL {
		next.AWTRIX.clockDiscovered = old.AWTRIX.clockDiscovered
	}
}

// swapDiscoveredClock makes to the effective clock URL, in memory only, if
// the effective URL is still from. It reports whether it swapped: a menu PUT
// or reload that landed while discovery was probing wins over the swap.
func (a *App) swapDiscoveredClock(from, to string) bool {
	swapped := false
	a.updateConfig(func(c *Config) {
		if c.effectiveClockURL() != from {
			return
		}
		c.AWTRIX.clockDiscovered = to
		swapped = true
	})
	return swapped
}

// deviceSource reports where the effective clock URL came from.
func (a *App) deviceSource() clockSource {
	_, src := a.cfg.Load().clockURL()
	return src
}

// clockConfigDTO is the /v1/device/config PUT body and the overlay DTO. A nil
// BaseURL (key omitted, or null) leaves the override as it is.
type clockConfigDTO struct {
	BaseURL *string `json:"base_url,omitempty"`
}

// deviceBaseURLKey is the settings-KV key holding the menu-chosen clock URL,
// stored as the raw URL string (the format it had before it joined the
// overlay, kept so the store needs no migration).
const deviceBaseURLKey = "device_base_url"

// clockSettingSpec registers the menu's clock URL with the settings overlay.
// There is no after hook: clockAccess resolves the URL on every call.
func clockSettingSpec() settingSpec[clockConfigDTO] {
	return settingSpec[clockConfigDTO]{
		key: deviceBaseURLKey,
		view: func(c Config) clockConfigDTO {
			if c.AWTRIX.clockOverride == "" {
				return clockConfigDTO{}
			}
			u := c.AWTRIX.clockOverride
			return clockConfigDTO{BaseURL: &u}
		},
		apply: func(c *Config, d clockConfigDTO) error {
			if d.BaseURL == nil {
				return nil
			}
			if err := validDeviceURL(*d.BaseURL); err != nil {
				return err
			}
			c.AWTRIX.clockOverride = *d.BaseURL
			return nil
		},
		encode: func(d clockConfigDTO) string {
			if d.BaseURL == nil {
				return ""
			}
			return *d.BaseURL
		},
		decode: func(blob string) []byte {
			if blob == "" {
				return []byte(`{}`)
			}
			b, _ := json.Marshal(clockConfigDTO{BaseURL: &blob})
			return b
		},
	}
}
