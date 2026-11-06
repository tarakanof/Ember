package main

import "encoding/json"

type clockSource = string

const (
	clockSourceStore      clockSource = "store"
	clockSourceConfig     clockSource = "config"
	clockSourceDiscovered clockSource = "discovered"
	clockSourceNone       clockSource = "none"
)

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

func (c *Config) effectiveClockURL() string {
	u, _ := c.clockURL()
	return u
}

func carryClockURL(old Config, next *Config) {
	next.AWTRIX.clockOverride = old.AWTRIX.clockOverride
	if next.AWTRIX.HTTPBaseURL == old.AWTRIX.HTTPBaseURL {
		next.AWTRIX.clockDiscovered = old.AWTRIX.clockDiscovered
	}
}

func (a *App) swapDiscoveredClock(from, to string) bool {
	swapped := false
	a.updateConfig(func(c *Config) {
		if c.effectiveClockURL() != from {
			return
		}
		if to == c.AWTRIX.clockOverride {
			c.AWTRIX.clockDiscovered = ""
		} else {
			c.AWTRIX.clockDiscovered = to
		}
		swapped = true
	})
	return swapped
}

func (a *App) deviceSource() clockSource {
	_, src := a.cfg.Load().clockURL()
	return src
}

type clockConfigDTO struct {
	BaseURL *string `json:"base_url,omitempty"`
}

const deviceBaseURLKey = "device_base_url"

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
