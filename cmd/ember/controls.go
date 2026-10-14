package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type controlGesture string

const (
	gesturePush       controlGesture = "push"
	gestureDoublePush controlGesture = "double_push"
	gestureLongPush   controlGesture = "long_push"
	gestureTurn       controlGesture = "turn"
)

const (
	controlsSettingsKey = "controls_json"
	maxControlTarget    = 64
	maxControlSteps     = 20
	maxQueuedSteps      = 100
)

type ControlsConfig struct {
	Target string `json:"target"`
}

func (c ControlsConfig) validate() error {
	if !utf8.ValidString(c.Target) || utf8.RuneCountInString(c.Target) > maxControlTarget {
		return fmt.Errorf("target must be at most %d characters", maxControlTarget)
	}
	return nil
}

func controlsSettingSpec() settingSpec[ControlsConfig] {
	return settingSpec[ControlsConfig]{
		key:  controlsSettingsKey,
		view: func(c Config) ControlsConfig { return c.Controls },
		apply: func(c *Config, d ControlsConfig) error {
			d.Target = strings.TrimSpace(d.Target)
			if err := d.validate(); err != nil {
				return err
			}
			c.Controls = d
			return nil
		},
	}
}

type controlsActionRequest struct {
	Gesture controlGesture `json:"gesture"`
	Steps   int            `json:"steps"`
}

func (r controlsActionRequest) validate() error {
	switch r.Gesture {
	case gesturePush, gestureDoublePush, gestureLongPush:
		if r.Steps != 0 {
			return errors.New("steps is for turn only")
		}
	case gestureTurn:
		if r.Steps == 0 || r.Steps < -maxControlSteps || r.Steps > maxControlSteps {
			return fmt.Errorf("turn needs steps of -%d..%d, not 0", maxControlSteps, maxControlSteps)
		}
	default:
		return errors.New("gesture must be push, double_push, long_push or turn")
	}
	return nil
}

func clampSteps(v int) int { return max(-maxQueuedSteps, min(maxQueuedSteps, v)) }
