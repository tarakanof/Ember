package render

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"strings"
)

type CoordOpts struct {
	WaitIcons bool
}

const waitIconBlinkCs = 50

var waitIconTools = []string{"claude", "codex", "t3"}

func WaitIconName(tool string) string {
	switch tool {
	case "codex", "t3":
		return "ember_wait_" + tool
	}
	return "ember_wait_claude"
}

func WaitIcons() (map[string][]byte, error) {
	out := make(map[string][]byte, len(waitIconTools))
	for _, tool := range waitIconTools {
		data, err := waitIconGIF(Session{Tool: tool})
		if err != nil {
			return nil, err
		}
		out[WaitIconName(tool)] = data
	}
	return out, nil
}

func waitIconGIF(s Session) ([]byte, error) {
	palette := color.Palette{color.Black, color.RGBA{colorWaiting.R, colorWaiting.G, colorWaiting.B, 0xff}}
	var f Frame
	drawToolIcon8(&f, s, colorWaiting, RGB{})
	on := image.NewPaletted(image.Rect(0, 0, iconW, 8), palette)
	for y := 0; y < 8; y++ {
		for x := 0; x < iconW; x++ {
			if f.Dirty[y][x] && f.Pixels[y][x] == colorWaiting {
				on.SetColorIndex(x, y, 1)
			}
		}
	}
	off := image.NewPaletted(image.Rect(0, 0, iconW, 8), palette)
	var buf bytes.Buffer
	err := gif.EncodeAll(&buf, &gif.GIF{
		Image: []*image.Paletted{on, off},
		Delay: []int{waitIconBlinkCs, waitIconBlinkCs},
	})
	return buf.Bytes(), err
}

func HostCount(sessions []Session) int {
	seen := map[string]bool{}
	for _, s := range sessions {
		if s.State == "idle" || s.Source == "" {
			continue
		}
		seen[strings.ToUpper(s.Source)] = true
	}
	return len(seen)
}

func DisplayName(s Session, hosts int) string {
	if s.Project == "" {
		return ""
	}
	if hosts > 1 && s.Source != "" {
		return strings.ToUpper(s.Source) + "/" + s.Project
	}
	return s.Project
}

func scrollLoopWhenLong() map[string]any {
	return map[string]any{"mode": "loop", "whenFits": "static"}
}

func waitingLockPayload(s Session, sessions []Session, name string, lifetimeSeconds int, icons bool) map[string]any {
	p := detailPayload(s, sessions, name, stateHex("waiting"), !icons, lifetimeSeconds, true)
	p["scroll"] = scrollLoopWhenLong()
	if icons {
		withNativeWaitIcon(p, s)
	}
	return p
}

func withNativeWaitIcon(p map[string]any, s Session) {
	draw, _ := p["draw"].([]any)
	p["draw"] = draw[1:]
	if len(draw) == 1 {
		delete(p, "draw")
	}
	delete(p, "textOffsetX")
	p["icon"] = WaitIconName(s.Tool)
}

func waitingCardPayload(s Session, sessions []Session, name string, lifetimeSeconds int) map[string]any {
	return readOnce(detailPayload(s, sessions, name, stateHex("waiting"), false, lifetimeSeconds, false))
}
