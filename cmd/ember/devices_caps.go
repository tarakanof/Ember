package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

const knobViewMajorMax = 1

const (
	capsSourceReported = "reported"
	capsSourceLegacy   = "legacy"
)

const (
	capsMaxPages     = 32
	capsMaxFeatures  = 64
	capsMaxViewMajor = 1000
	capsMaxLimit     = 1 << 24
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
	View     []int             `json:"view"`
	Pages    []string          `json:"pages"`
	Features []string          `json:"features"`
	Limits   *deviceCapsLimits `json:"limits,omitempty"`
}

type deviceCapsLimits struct {
	ViewBytes   int `json:"view_bytes,omitempty"`
	ConfigBytes int `json:"config_bytes,omitempty"`
}

type effectiveCapsView struct {
	View     []int             `json:"view"`
	Pages    []string          `json:"pages"`
	Features []string          `json:"features"`
	Limits   *deviceCapsLimits `json:"limits,omitempty"`
	Source   string            `json:"source"`
}

func (c *deviceCaps) clone() *deviceCaps {
	if c == nil {
		return nil
	}
	out := &deviceCaps{View: slices.Clone(c.View), Pages: slices.Clone(c.Pages), Features: slices.Clone(c.Features)}
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
	return slices.Equal(c.View, o.View) && slices.Equal(c.Pages, o.Pages) && slices.Equal(c.Features, o.Features) &&
		c.limits() == o.limits()
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
	l := c.limits()
	if l.ViewBytes < 0 || l.ViewBytes > capsMaxLimit || l.ConfigBytes < 0 || l.ConfigBytes > capsMaxLimit {
		return fmt.Errorf("limits must be 0..%d bytes", capsMaxLimit)
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
	if out.Limits != nil && *out.Limits == (deviceCapsLimits{}) {
		out.Limits = nil
	}
	return out
}

func (c *deviceCaps) hasPage(id string) bool { return slices.Contains(c.Pages, id) }

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
	return &effectiveCapsView{View: c.View, Pages: c.Pages, Features: c.Features, Limits: c.Limits, Source: source}
}

func checkConfigAgainstCaps(caps *deviceCaps, before, after knobSettings, size int) error {
	if caps == nil {
		return nil
	}
	for _, p := range after.Pages {
		if p.On && !caps.hasPage(p.ID) && !before.pageOn(p.ID) {
			return fmt.Errorf("%w: page %q is not in this knob's firmware (caps.pages)", errSettingBody, p.ID)
		}
	}
	if limit := caps.limits().ConfigBytes; limit > 0 && size > limit {
		return fmt.Errorf("%w: config is %d bytes, over this knob's %d (caps.limits.config_bytes)", errSettingBody, size, limit)
	}
	return nil
}
