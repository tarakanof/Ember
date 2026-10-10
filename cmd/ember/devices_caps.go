package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const knobViewMajorMax = 1

const (
	capsSourceReported = "reported"
	capsSourceLegacy   = "legacy"
)

const (
	capsMaxPages     = 32
	capsMaxRotations = 4
	capsMaxFeatures  = 64
	capsMaxViewMajor = 1000
	capsMaxLimit     = 1 << 24
	capsMinViewBytes = 4096
	capsErrorMaxLen  = 160
)

const (
	featureViewWait       = "view_wait"
	featureNPControl      = "np_control"
	featureOTARollback    = "ota_rollback"
	featureCoredump       = "coredump"
	featureStatsIntervals = "stats_intervals"
)

var capsFeaturePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type deviceCaps struct {
	View      []int             `json:"view"`
	Pages     []string          `json:"pages"`
	Features  []string          `json:"features"`
	Limits    *deviceCapsLimits `json:"limits,omitempty"`
	Rotations []int             `json:"rotations,omitempty"`
}

type deviceCapsLimits struct {
	ViewBytes   int `json:"view_bytes,omitempty"`
	ConfigBytes int `json:"config_bytes,omitempty"`
}

type effectiveCapsView struct {
	View      []int             `json:"view"`
	Pages     []string          `json:"pages"`
	Features  []string          `json:"features"`
	Limits    *deviceCapsLimits `json:"limits,omitempty"`
	Rotations []int             `json:"rotations"`
	Source    string            `json:"source"`
	CapsError string            `json:"caps_error,omitempty"`
}

func (c *deviceCaps) clone() *deviceCaps {
	if c == nil {
		return nil
	}
	out := &deviceCaps{View: slices.Clone(c.View), Pages: slices.Clone(c.Pages), Features: slices.Clone(c.Features),
		Rotations: slices.Clone(c.Rotations)}
	if c.Limits != nil {
		l := *c.Limits
		out.Limits = &l
	}
	return out
}

func (c *deviceCaps) equal(o *deviceCaps) bool {
	if c == nil || o == nil {
		return c == o
	}
	return slices.Equal(c.View, o.View) && slices.Equal(sortedCopy(c.Pages), sortedCopy(o.Pages)) &&
		slices.Equal(c.Features, o.Features) && c.limits() == o.limits() && slices.Equal(c.rotations(), o.rotations())
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

func (c *deviceCaps) limits() deviceCapsLimits {
	if c == nil || c.Limits == nil {
		return deviceCapsLimits{}
	}
	return *c.Limits
}

func (c deviceCaps) validate() error {
	switch {
	case len(c.View) != 2:
		return errors.New("view must be [min, max]")
	case c.View[0] < 1 || c.View[0] > c.View[1] || c.View[1] > capsMaxViewMajor:
		return fmt.Errorf("view must be [min, max] with 1 <= min <= max <= %d", capsMaxViewMajor)
	case len(c.Pages) == 0 || len(c.Pages) > capsMaxPages:
		return fmt.Errorf("pages must list 1..%d page ids", capsMaxPages)
	case len(c.Features) > capsMaxFeatures:
		return fmt.Errorf("features must list at most %d tokens", capsMaxFeatures)
	}
	if err := uniqueMatching("page", c.Pages, knobPageIDPattern); err != nil {
		return err
	}
	if err := uniqueMatching("feature", c.Features, capsFeaturePattern); err != nil {
		return err
	}
	if err := c.validateRotations(); err != nil {
		return err
	}
	l := c.limits()
	if l.ViewBytes < 0 || l.ViewBytes > capsMaxLimit || l.ConfigBytes < 0 || l.ConfigBytes > capsMaxLimit {
		return fmt.Errorf("limits must be 0..%d bytes", capsMaxLimit)
	}
	if l.ViewBytes > 0 && l.ViewBytes < capsMinViewBytes {
		return fmt.Errorf("limits.view_bytes must be 0 or at least %d", capsMinViewBytes)
	}
	if floor := defaultKnobConfigBytes(); l.ConfigBytes > 0 && l.ConfigBytes < floor {
		return fmt.Errorf("limits.config_bytes must be 0 or at least %d, the default config", floor)
	}
	return nil
}

func (c deviceCaps) validateRotations() error {
	if len(c.Rotations) == 0 {
		return nil
	}
	if len(c.Rotations) > capsMaxRotations || !slices.Contains(c.Rotations, 0) {
		return fmt.Errorf("rotations must list 0 and at most %d of %v", capsMaxRotations, knobRotations)
	}
	for i, r := range c.Rotations {
		if !slices.Contains(knobRotations, r) {
			return fmt.Errorf("rotation %d must be one of %v", r, knobRotations)
		}
		if slices.Contains(c.Rotations[:i], r) {
			return fmt.Errorf("rotation %d listed twice", r)
		}
	}
	return nil
}

func defaultKnobConfigBytes() int {
	b, _ := json.Marshal(defaultKnobSettings())
	return len(b)
}

func decodeCaps(raw json.RawMessage) (*deviceCaps, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ""
	}
	var c deviceCaps
	err := json.Unmarshal(raw, &c)
	if err == nil {
		err = rejectNullRotations(raw)
	}
	if err == nil {
		err = c.validate()
	}
	if err != nil {
		msg := err.Error()
		if len(msg) > capsErrorMaxLen {
			msg = strings.ToValidUTF8(msg[:capsErrorMaxLen], "")
		}
		return nil, msg
	}
	return c.normalized(), ""
}

func rejectNullRotations(raw json.RawMessage) error {
	var probe struct {
		Rotations []json.RawMessage `json:"rotations"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	for _, r := range probe.Rotations {
		if string(r) == "null" {
			return errors.New("rotations must list integers, not null")
		}
	}
	return nil
}

func uniqueMatching(what string, ids []string, pattern *regexp.Regexp) error {
	for i, id := range ids {
		if !pattern.MatchString(id) {
			return fmt.Errorf("%s %q must match %s", what, id, pattern)
		}
		if slices.Contains(ids[:i], id) {
			return fmt.Errorf("%s %q listed twice", what, id)
		}
	}
	return nil
}

func (c deviceCaps) normalized() *deviceCaps {
	out := c.clone()
	if out.Features == nil {
		out.Features = []string{}
	}
	slices.Sort(out.Features)
	slices.Sort(out.Rotations)
	if len(out.Rotations) == 0 {
		out.Rotations = nil
	}
	if out.Limits != nil && *out.Limits == (deviceCapsLimits{}) {
		out.Limits = nil
	}
	return out
}

func (c *deviceCaps) hasPage(id string) bool { return slices.Contains(c.Pages, id) }

func (c *deviceCaps) rotations() []int {
	if c == nil || len(c.Rotations) == 0 {
		return []int{0}
	}
	return c.Rotations
}

func (c *deviceCaps) viewMajor() int { return min(knobViewMajorMax, c.View[1]) }

type legacyCapsRow struct {
	minFW    string
	pages    []string
	features []string
	limits   *deviceCapsLimits
}

var legacyKnobCapsBasePages = []string{"bot", "pomodoro", "weather"}

var legacyKnobCaps = []legacyCapsRow{
	{minFW: "0.7.0", features: []string{featureStatsIntervals}},
	{minFW: "0.8.0", features: []string{featureViewWait}},
	{minFW: "0.9.0", pages: []string{knobNowPlayingPage}},
	{minFW: "0.9.6", features: []string{featureNPControl}},
	{minFW: "0.9.14", features: []string{featureCoredump}},
	{minFW: "0.9.16", features: []string{featureOTARollback}},
	{minFW: "0.9.28", limits: &deviceCapsLimits{ViewBytes: 16383, ConfigBytes: 1024}},
}

func legacyCaps(fw string) deviceCaps {
	c := deviceCaps{View: []int{1, 1}, Pages: slices.Clone(legacyKnobCapsBasePages), Features: []string{}}
	fw, _, _ = strings.Cut(fw, "+")
	if !semverPattern.MatchString(fw) {
		return c
	}
	for _, row := range legacyKnobCaps {
		if compareSemver(fw, row.minFW) < 0 {
			break
		}
		c.Pages = append(c.Pages, row.pages...)
		c.Features = append(c.Features, row.features...)
		if row.limits != nil {
			l := *row.limits
			c.Limits = &l
		}
	}
	return c
}

func effectiveCaps(d deviceRecord) (deviceCaps, string) {
	if d.Caps != nil {
		return *d.Caps.clone(), capsSourceReported
	}
	fw := ""
	if d.LastCheckin != nil {
		fw = d.LastCheckin.FW
	}
	return legacyCaps(fw), capsSourceLegacy
}

func (d deviceRecord) effectiveCapsView() *effectiveCapsView {
	if d.Kind != deviceKindKnob {
		return nil
	}
	c, source := effectiveCaps(d)
	return &effectiveCapsView{View: c.View, Pages: c.Pages, Features: c.Features, Limits: c.Limits,
		Rotations: slices.Clone(c.rotations()), Source: source, CapsError: d.CapsError}
}

func checkConfigAgainstCaps(caps *deviceCaps, before, after knobSettings, beforeSize, size int) error {
	if caps == nil {
		return nil
	}
	for _, p := range after.Pages {
		if p.On && !caps.hasPage(p.ID) && !before.pageOn(p.ID) {
			return fmt.Errorf("%w: page %q is not in this knob's firmware (caps.pages)", errSettingBody, p.ID)
		}
	}
	if r := after.Display.Rotation; r != before.Display.Rotation && !slices.Contains(caps.rotations(), r) {
		return fmt.Errorf("%w: display.rotation %d is not supported by this knob (caps.rotations %v)", errSettingBody, r, caps.rotations())
	}
	if limit := caps.limits().ConfigBytes; limit > 0 && size > limit && size > beforeSize {
		return fmt.Errorf("%w: config is %d bytes, over this knob's %d (caps.limits.config_bytes)", errSettingBody, size, limit)
	}
	return nil
}
