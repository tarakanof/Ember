package devices

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const KnobViewMajorMax = 1

const (
	CapsSourceReported = "reported"
	CapsSourceLegacy   = "legacy"
)

const (
	capsMaxPages     = 32
	capsMaxRotations = 4
	capsMaxFeatures  = 64
	capsMaxViewMajor = 1000
	capsMaxLimit     = 1 << 24
	CapsMinViewBytes = 4096
)

const (
	FeatureViewWait       = "view_wait"
	featureNPControl      = "np_control"
	featureOTARollback    = "ota_rollback"
	featureCoredump       = "coredump"
	featureStatsIntervals = "stats_intervals"
)

var capsFeaturePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type Caps struct {
	View      []int       `json:"view"`
	Pages     []string    `json:"pages"`
	Features  []string    `json:"features"`
	Limits    *CapsLimits `json:"limits,omitempty"`
	Rotations []int       `json:"rotations,omitempty"`
}

type CapsLimits struct {
	ViewBytes   int `json:"view_bytes,omitempty"`
	ConfigBytes int `json:"config_bytes,omitempty"`
}

type effectiveCapsView struct {
	View      []int       `json:"view"`
	Pages     []string    `json:"pages"`
	Features  []string    `json:"features"`
	Limits    *CapsLimits `json:"limits,omitempty"`
	Rotations []int       `json:"rotations"`
	Source    string      `json:"source"`
	CapsError string      `json:"caps_error,omitempty"`
}

func (c *Caps) Clone() *Caps {
	if c == nil {
		return nil
	}
	out := &Caps{View: slices.Clone(c.View), Pages: slices.Clone(c.Pages), Features: slices.Clone(c.Features),
		Rotations: slices.Clone(c.Rotations)}
	if c.Limits != nil {
		l := *c.Limits
		out.Limits = &l
	}
	return out
}

func (c *Caps) equal(o *Caps) bool {
	if c == nil || o == nil {
		return c == o
	}
	return slices.Equal(c.View, o.View) && slices.Equal(sortedCopy(c.Pages), sortedCopy(o.Pages)) &&
		slices.Equal(c.Features, o.Features) && c.EffectiveLimits() == o.EffectiveLimits() && slices.Equal(c.rotations(), o.rotations())
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

func (c *Caps) EffectiveLimits() CapsLimits {
	if c == nil || c.Limits == nil {
		return CapsLimits{}
	}
	return *c.Limits
}

func (c Caps) Validate() error {
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
	l := c.EffectiveLimits()
	if l.ViewBytes < 0 || l.ViewBytes > capsMaxLimit || l.ConfigBytes < 0 || l.ConfigBytes > capsMaxLimit {
		return fmt.Errorf("limits must be 0..%d bytes", capsMaxLimit)
	}
	if l.ViewBytes > 0 && l.ViewBytes < CapsMinViewBytes {
		return fmt.Errorf("limits.view_bytes must be 0 or at least %d", CapsMinViewBytes)
	}
	if floor := DefaultKnobConfigBytes(); l.ConfigBytes > 0 && l.ConfigBytes < floor {
		return fmt.Errorf("limits.config_bytes must be 0 or at least %d, the default config", floor)
	}
	return nil
}

func (c Caps) validateRotations() error {
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

func DefaultKnobConfigBytes() int {
	b, _ := json.Marshal(DefaultKnobSettings())
	return len(b)
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

func (c Caps) Normalized() *Caps {
	out := c.Clone()
	if out.Features == nil {
		out.Features = []string{}
	}
	slices.Sort(out.Features)
	slices.Sort(out.Rotations)
	if len(out.Rotations) == 0 {
		out.Rotations = nil
	}
	if out.Limits != nil && *out.Limits == (CapsLimits{}) {
		out.Limits = nil
	}
	return out
}

func (c *Caps) HasPage(id string) bool { return slices.Contains(c.Pages, id) }

func (c *Caps) HasFeature(token string) bool {
	return c != nil && slices.Contains(c.Features, token)
}

func (c *Caps) rotations() []int {
	if c == nil || len(c.Rotations) == 0 {
		return []int{0}
	}
	return c.Rotations
}

func (c *Caps) ViewMajor() int { return min(KnobViewMajorMax, c.View[1]) }

type legacyCapsRow struct {
	minFW    string
	pages    []string
	features []string
	limits   *CapsLimits
}

var legacyKnobCapsBasePages = []string{"bot", "pomodoro", "weather"}

var legacyKnobCaps = []legacyCapsRow{
	{minFW: "0.7.0", features: []string{featureStatsIntervals}},
	{minFW: "0.8.0", features: []string{FeatureViewWait}},
	{minFW: "0.9.0", pages: []string{KnobNowPlayingPage}},
	{minFW: "0.9.6", features: []string{featureNPControl}},
	{minFW: "0.9.14", features: []string{featureCoredump}},
	{minFW: "0.9.16", features: []string{featureOTARollback}},
	{minFW: "0.9.28", limits: &CapsLimits{ViewBytes: 16383, ConfigBytes: 1024}},
}

func legacyCaps(fw string) Caps {
	c := Caps{View: []int{1, 1}, Pages: slices.Clone(legacyKnobCapsBasePages), Features: []string{}}
	fw, _, _ = strings.Cut(fw, "+")
	if !SemverPattern.MatchString(fw) {
		return c
	}
	for _, row := range legacyKnobCaps {
		if CompareSemver(fw, row.minFW) < 0 {
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

func effectiveCaps(d Record) (Caps, string) {
	if d.Caps != nil {
		return *d.Caps.Clone(), CapsSourceReported
	}
	fw := ""
	if d.LastCheckin != nil {
		fw = d.LastCheckin.FW
	}
	return legacyCaps(fw), CapsSourceLegacy
}

func (d Record) effectiveCapsView() *effectiveCapsView {
	if d.Kind != KindKnob {
		return nil
	}
	c, source := effectiveCaps(d)
	return &effectiveCapsView{View: c.View, Pages: c.Pages, Features: c.Features, Limits: c.Limits,
		Rotations: slices.Clone(c.rotations()), Source: source, CapsError: d.CapsError}
}

func checkConfigAgainstCaps(caps *Caps, before, after KnobSettings, beforeSize, size int) error {
	if caps == nil {
		return nil
	}
	for _, p := range after.Pages {
		if p.On && !caps.HasPage(p.ID) && !before.PageOn(p.ID) {
			return fmt.Errorf("%w: page %q is not in this knob's firmware (caps.pages)", ErrSettingBody, p.ID)
		}
	}
	if r := after.Display.Rotation; r != before.Display.Rotation && !slices.Contains(caps.rotations(), r) {
		return fmt.Errorf("%w: display.rotation %d is not supported by this knob (caps.rotations %v)", ErrSettingBody, r, caps.rotations())
	}
	if limit := caps.EffectiveLimits().ConfigBytes; limit > 0 && size > limit && size > beforeSize {
		return fmt.Errorf("%w: config is %d bytes, over this knob's %d (caps.limits.config_bytes)", ErrSettingBody, size, limit)
	}
	return nil
}
