package render

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CoordOpts struct {
	WaitIcons bool
}

const (
	waitIconBlinkCs = 50
	waitIconPrefix  = "ember_wait_"
)

var waitIconTools = []string{"claude", "codex", "t3"}

func WaitIconName(tool string) string {
	switch tool {
	case "codex", "t3":
	default:
		tool = "claude"
	}
	icons, _ := WaitIcons()
	for name := range icons {
		if strings.HasPrefix(name, waitIconPrefix+tool+"_") {
			return name
		}
	}
	return waitIconPrefix + tool
}

var waitIconSet = sync.OnceValues(func() (map[string][]byte, error) {
	out := make(map[string][]byte, len(waitIconTools))
	for _, tool := range waitIconTools {
		data, err := waitIconGIF(Session{Tool: tool})
		if err != nil {
			return nil, err
		}
		out[fmt.Sprintf("%s%s_%08x", waitIconPrefix, tool, crc32.ChecksumIEEE(data))] = data
	}
	return out, nil
})

func WaitIcons() (map[string][]byte, error) { return waitIconSet() }

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

func waitingLabel(s Session, sessions []Session) string {
	name := DisplayName(s, HostCount(sessions))
	if name == "" || s.State != "waiting" {
		return name
	}
	others := -1
	for _, o := range sessions {
		if o.State == "waiting" {
			others++
		}
	}
	if others > 0 {
		name += " +" + strconv.Itoa(others)
	}
	return name
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

const (
	ngScrollPxPerSec = 21
	ngScrollHold     = time.Second
)

func textWidthPx(text string) int {
	w := 0
	for i, r := range strings.ToUpper(text) {
		if i > 0 {
			w++
		}
		w += ngGlyphW(r)
	}
	return w
}

func CardDwell(snap Snapshot, s Session, u *UsageView, card int) time.Duration {
	cards := AvailableCards(s, u)
	if card < 0 || card >= len(cards) || cards[card] != cardProject {
		return 0
	}
	w := textWidthPx(waitingLabel(s, snap.Sessions))
	if w <= panelW-contentX {
		return 0
	}
	return ngScrollHold + time.Duration(w+panelW-contentX)*time.Second/ngScrollPxPerSec
}
