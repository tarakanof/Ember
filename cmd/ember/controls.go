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

type controlKind string

const (
	controlNone        controlKind = "none"
	controlMedia       controlKind = "media"
	controlMute        controlKind = "mute"
	controlShortcut    controlKind = "shortcut"
	controlAppleScript controlKind = "applescript"
	controlVolume      controlKind = "volume"
	controlScroll      controlKind = "scroll"
)

const (
	controlsSettingsKey  = "controls_json"
	maxControlTarget     = 64
	maxShortcutName      = 128
	maxAppleScriptBytes  = 4096
	maxControlSteps      = 20
	maxControlDelta      = 100
	volumeStepMax        = 20
	scrollLinesMax       = 10
	defaultVolumeStep    = 2
	defaultScrollLines   = 3
	scrollAxisVertical   = "vertical"
	scrollAxisHorizontal = "horizontal"
	mediaKeyPlayPause    = "play_pause"
	mediaKeyNext         = "next"
	mediaKeyPrevious     = "previous"
)

type ControlAction struct {
	Kind   controlKind `json:"kind"`
	Key    string      `json:"key,omitempty"`
	Name   string      `json:"name,omitempty"`
	Script string      `json:"script,omitempty"`
	Step   int         `json:"step,omitempty"`
	Lines  int         `json:"lines,omitempty"`
	Axis   string      `json:"axis,omitempty"`
}

type ControlsConfig struct {
	Target     string        `json:"target"`
	Push       ControlAction `json:"push"`
	DoublePush ControlAction `json:"double_push"`
	LongPush   ControlAction `json:"long_push"`
	Turn       ControlAction `json:"turn"`
}

func defaultControlAction(g controlGesture) ControlAction {
	switch g {
	case gesturePush:
		return ControlAction{Kind: controlMedia, Key: mediaKeyPlayPause}
	case gestureTurn:
		return ControlAction{Kind: controlVolume, Step: defaultVolumeStep}
	}
	return ControlAction{Kind: controlNone}
}

func (c ControlsConfig) withDefaults() ControlsConfig {
	for _, g := range []controlGesture{gesturePush, gestureDoublePush, gestureLongPush, gestureTurn} {
		a := c.slot(g)
		if a.Kind == "" {
			*a = defaultControlAction(g)
		}
		*a = a.normalized()
	}
	return c
}

func (c *ControlsConfig) slot(g controlGesture) *ControlAction {
	switch g {
	case gesturePush:
		return &c.Push
	case gestureDoublePush:
		return &c.DoublePush
	case gestureLongPush:
		return &c.LongPush
	case gestureTurn:
		return &c.Turn
	}
	return nil
}

func (c ControlsConfig) action(g controlGesture) ControlAction {
	c = c.withDefaults()
	if a := c.slot(g); a != nil {
		return *a
	}
	return ControlAction{Kind: controlNone}
}

func (a ControlAction) normalized() ControlAction {
	switch a.Kind {
	case controlMedia:
		return ControlAction{Kind: a.Kind, Key: a.Key}
	case controlShortcut:
		return ControlAction{Kind: a.Kind, Name: strings.TrimSpace(a.Name)}
	case controlAppleScript:
		return ControlAction{Kind: a.Kind, Script: a.Script}
	case controlVolume:
		if a.Step == 0 {
			a.Step = defaultVolumeStep
		}
		return ControlAction{Kind: a.Kind, Step: a.Step}
	case controlScroll:
		if a.Lines == 0 {
			a.Lines = defaultScrollLines
		}
		if a.Axis == "" {
			a.Axis = scrollAxisVertical
		}
		return ControlAction{Kind: a.Kind, Lines: a.Lines, Axis: a.Axis}
	}
	return ControlAction{Kind: a.Kind}
}

func (a ControlAction) validate(g controlGesture) error {
	turn := g == gestureTurn
	switch a.Kind {
	case controlNone:
		return nil
	case controlMedia, controlMute, controlShortcut, controlAppleScript:
		if turn {
			return fmt.Errorf("%s: %s is for presses, not turn", g, a.Kind)
		}
	case controlVolume, controlScroll:
		if !turn {
			return fmt.Errorf("%s: %s is for turn only", g, a.Kind)
		}
	default:
		return fmt.Errorf("%s: kind must be none, media, mute, shortcut, applescript, volume or scroll", g)
	}
	switch a.Kind {
	case controlMedia:
		switch a.Key {
		case mediaKeyPlayPause, mediaKeyNext, mediaKeyPrevious:
		default:
			return fmt.Errorf("%s: media key must be play_pause, next or previous", g)
		}
	case controlShortcut:
		if a.Name == "" || !utf8.ValidString(a.Name) || utf8.RuneCountInString(a.Name) > maxShortcutName {
			return fmt.Errorf("%s: shortcut name must be 1 to %d characters", g, maxShortcutName)
		}
	case controlAppleScript:
		if strings.TrimSpace(a.Script) == "" || !utf8.ValidString(a.Script) || len(a.Script) > maxAppleScriptBytes {
			return fmt.Errorf("%s: script must be 1 to %d bytes", g, maxAppleScriptBytes)
		}
	case controlVolume:
		if a.Step < 1 || a.Step > volumeStepMax {
			return fmt.Errorf("%s: volume step must be 1-%d", g, volumeStepMax)
		}
	case controlScroll:
		if a.Lines < 1 || a.Lines > scrollLinesMax {
			return fmt.Errorf("%s: scroll lines must be 1-%d", g, scrollLinesMax)
		}
		if a.Axis != scrollAxisVertical && a.Axis != scrollAxisHorizontal {
			return fmt.Errorf("%s: scroll axis must be vertical or horizontal", g)
		}
	}
	return nil
}

func (c ControlsConfig) validate() error {
	if !utf8.ValidString(c.Target) || utf8.RuneCountInString(c.Target) > maxControlTarget {
		return fmt.Errorf("target must be at most %d characters", maxControlTarget)
	}
	for _, g := range []controlGesture{gesturePush, gestureDoublePush, gestureLongPush, gestureTurn} {
		if err := c.slot(g).validate(g); err != nil {
			return err
		}
	}
	return nil
}

func controlsSettingSpec() settingSpec[ControlsConfig] {
	return settingSpec[ControlsConfig]{
		key:  controlsSettingsKey,
		view: func(c Config) ControlsConfig { return c.Controls.withDefaults() },
		apply: func(c *Config, d ControlsConfig) error {
			d.Target = strings.TrimSpace(d.Target)
			d = d.withDefaults()
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

func resolveControl(a ControlAction, steps int) controlCommand {
	cmd := controlCommand{Kind: a.Kind, Key: a.Key, Name: a.Name, Script: a.Script}
	switch a.Kind {
	case controlVolume:
		cmd.Delta = clampDelta(steps * a.Step)
	case controlScroll:
		cmd.Axis = a.Axis
		cmd.Delta = clampDelta(steps * a.Lines)
	}
	return cmd
}

func clampDelta(v int) int { return max(-maxControlDelta, min(maxControlDelta, v)) }
