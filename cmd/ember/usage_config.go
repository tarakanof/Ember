package main

import (
	"fmt"
	"net/http"
)

// usageSettingsKey is the writable-store key for the runtime usage-widget
// toggles (overrides the config.json baseline via the settings overlay).
const usageSettingsKey = "usage_json"

type usageConfigDTO struct {
	UsageWidget       bool `json:"usage_widget"`
	UsagePerModel     bool `json:"usage_per_model"`
	LimitAlarm        bool `json:"limit_alarm"`
	UsageThresholdPct int  `json:"usage_threshold_pct"`
}

// usageSettingSpec registers the usage-widget toggles with the settings
// overlay. The coordinator reads usageWidgetEnabled()/usagePerModelEnabled()/
// limitAlarmEnabled() live; the after hook nudges a prompt re-render so the
// toggle shows quickly.
func (a *App) usageSettingSpec() settingSpec[usageConfigDTO] {
	return settingSpec[usageConfigDTO]{
		key: usageSettingsKey,
		view: func(c Config) usageConfigDTO {
			return usageConfigDTO{
				UsageWidget:       c.usageWidgetEnabled(),
				UsagePerModel:     c.usagePerModelEnabled(),
				LimitAlarm:        c.limitAlarmEnabled(),
				UsageThresholdPct: c.usageThresholdPct(),
			}
		},
		apply: func(c *Config, d usageConfigDTO) error {
			if d.UsageThresholdPct < 0 || d.UsageThresholdPct > 100 {
				return fmt.Errorf("usage_threshold_pct %d out of range [0, 100]", d.UsageThresholdPct)
			}
			c.UsageWidget = &d.UsageWidget
			c.UsagePerModel = &d.UsagePerModel
			c.LimitAlarm = &d.LimitAlarm
			c.UsageThresholdPct = &d.UsageThresholdPct
			return nil
		},
		after: func(Config) { a.nudgePomo() },
	}
}

func (a *App) handleUsageConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.usage)
}

func (a *App) handleUsageConfigPut(w http.ResponseWriter, r *http.Request) {
	if d, ok := serveSettingPut(a, w, r, a.settings.usage); ok {
		writeJSON(w, http.StatusOK, d)
	}
}
