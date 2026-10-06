package render

import (
	"fmt"
	"time"
)

const (
	samplePct      = 47
	sampleActivity = "Bash: npm test"
)

func SampleBaseSession() Session {
	return Session{Source: "mbp", Tool: "claude", Session: "sample", State: "running"}
}

func SampleUsageView() *UsageView {
	p := 42
	return &UsageView{FiveHourPct: 87, ResetLabel: "17:30", SevenDayPct: &p}
}

func ptrInt(v int) *int { return &v }

type DraftDisplay struct {
	ContextPct     bool
	RateBottomBar  bool
	ActivityDetail bool
	SourceCard     bool
	SessionBar     bool
	SourceColor    string
}

func PreviewSession(d DraftDisplay, base Session) Session {
	s := base

	if d.ContextPct {
		if s.ContextPct == nil {
			s.ContextPct = ptrInt(samplePct)
		}
	} else {
		s.ContextPct = nil
	}

	if d.RateBottomBar {
		if s.RateWindowPct == nil {
			s.RateWindowPct = ptrInt(samplePct)
		}
	} else {
		s.RateWindowPct = nil
	}
	s.RateBottomBar = d.RateBottomBar

	if d.ActivityDetail {
		if s.Activity == "" {
			s.Activity = sampleActivity
		}
	} else {
		s.Activity = ""
	}

	if d.SourceColor != "" {
		c := d.SourceColor
		s.SourceColor = &c
	} else {
		s.SourceColor = nil
	}

	s.SourceCard = &d.SourceCard
	s.SessionBar = &d.SessionBar

	return s
}

type CardFrame struct {
	Card   string   `json:"card"`
	Pixels []string `json:"pixels"`
}

type Preview struct {
	Width    int         `json:"width"`
	Height   int         `json:"height"`
	Activity string      `json:"activity"`
	Frames   []CardFrame `json:"frames"`
}

func HexPixels(f *Frame) []string {
	approx := withNativeApproximated(f)
	return hexPixels(&approx)
}

func PreviewFrames(s Session, u *UsageView, now time.Time) Preview {
	p := Preview{Width: 32, Height: 8, Frames: []CardFrame{}}
	for _, c := range AvailableCards(s, u) {
		if c == cardTool {
			p.Activity = s.Activity
			continue
		}
		frame := ComposeFrame(s, c, u, []Session{s}, now)
		p.Frames = append(p.Frames, CardFrame{Card: cardName(c), Pixels: HexPixels(&frame)})
	}
	return p
}

func cardName(c int) string {
	switch c {
	case cardSource:
		return "source"
	case cardTool:
		return "tool"
	case cardUsage5h:
		return "usage-5h"
	case cardUsageReset:
		return "usage-reset"
	case cardUsage7d:
		return "usage-7d"
	case cardUsageModelA:
		return "usage-model-a"
	case cardUsageModelB:
		return "usage-model-b"
	default:
		panic(fmt.Sprintf("cardName: unknown card const %d", c))
	}
}

func withNativeApproximated(f *Frame) Frame {
	out := *f
	if n := f.Native; n != nil && n.Text != "" {
		drawDigits(&out, n.Text, n.X, 1, n.Color)
	}
	return out
}

func hexPixels(f *Frame) []string {
	ints := framePixels(f)
	out := make([]string, len(ints))
	for i, v := range ints {
		out[i] = fmt.Sprintf("#%06x", v)
	}
	return out
}
