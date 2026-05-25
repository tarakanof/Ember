//go:build darwin

package main

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/dt/awtrix-ai-status/internal/render"

	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
)

// previewScale is the matrix-pixel -> screen-pixel block size for the preview.
const previewScale = 7

// previewWidget owns the preview image view, its rotation state, and the inputs
// the rotation timer needs to re-render. formProvider returns the current live
// control state; base is the session fetched from /state (or a sample).
type previewWidget struct {
	imageView    appkit.ImageView
	caption      appkit.TextField
	badge        appkit.TextField
	formProvider func() settingsForm
	base         render.Session
	live         bool
	cursor       int
	timer        foundation.Timer

	// pixBuf retains the byte buffer backing the currently displayed image.
	// NSBitmapImageRep references (does not copy) the planes we hand it, so
	// this must stay reachable or AppKit will read freed memory.
	pixBuf []byte
}

// rgbaToImage builds an NSImage from a width x height 8-bit RGBA buffer. The
// caller must retain pix for as long as the returned image is in use.
func rgbaToImage(pix []byte, w, h int) appkit.Image {
	rep := appkit.NewBitmapImageRepWithBitmapDataPlanesPixelsWidePixelsHighBitsPerSampleSamplesPerPixelHasAlphaIsPlanarColorSpaceNameBytesPerRowBitsPerPixel(
		unsafe.Pointer(&pix[0]), w, h, 8, 4, true, false,
		appkit.DeviceRGBColorSpace, w*4, 32,
	)
	img := appkit.NewImageWithSize(foundation.Size{Width: float64(w), Height: float64(h)})
	img.AddRepresentation(rep)
	return img
}

// render re-composes the current frame from the live form + base session and
// updates the image, caption, and badge.
func (p *previewWidget) render() {
	f := p.formProvider()
	sess := previewSession(f, p.base)

	cards := render.AvailableCards(sess)
	if len(cards) == 0 {
		cards = []int{0}
	}
	card := cards[p.cursor%len(cards)]

	robotColor := render.RGB{R: 0xff, G: 0xff, B: 0xff}
	if rgb, ok := hexToRGB(f.SourceColor); ok {
		robotColor = rgb
	}

	frame := render.ComposeFrame(sess, 1, 1, card, robotColor, []render.Session{sess}, time.Now())
	pix, w, h := render.RenderRGBA(frame, previewScale)
	p.pixBuf = pix // retain before the image references it
	p.imageView.SetImage(rgbaToImage(pix, w, h))
	p.caption.SetStringValue(cardCaption(card))
	if p.live {
		p.badge.SetStringValue("● live")
	} else {
		p.badge.SetStringValue("sample")
	}
}

// cardCaption maps a render card constant to a human label. The constants are
// unexported in internal/render; their iota order is cardXY=0, cardRate=1,
// cardTool=2, cardCtx=3, cardReset=4.
func cardCaption(card int) string {
	switch card {
	case 1:
		return "5h rate-limit % used"
	case 2:
		return "tool / approval detail (scrolls as text)"
	case 3:
		return "context-window % used"
	case 4:
		return "hours until 5h reset"
	default:
		return "X / Y session index"
	}
}

// onFormChanged resets the rotation to the first card and re-renders, so a
// just-toggled element is immediately visible.
func (p *previewWidget) onFormChanged() {
	p.cursor = 0
	p.render()
}

// startRotation advances the number slot every 1.6s on the main run loop.
func (p *previewWidget) startRotation() {
	p.timer = foundation.Timer_ScheduledTimerWithTimeIntervalRepeatsBlock(1.6, true, func(t foundation.Timer) {
		p.cursor++
		p.render()
	})
}

// stop invalidates the rotation timer; safe to call once on window close.
func (p *previewWidget) stop() {
	if !p.timer.IsNil() {
		p.timer.Invalidate()
	}
}

// hexToRGB parses "#RRGGBB" into a render.RGB. ok is false for any other form.
func hexToRGB(s string) (render.RGB, bool) {
	if len(s) != 7 || s[0] != '#' {
		return render.RGB{}, false
	}
	var r, g, b int
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return render.RGB{}, false
	}
	return render.RGB{R: uint8(r), G: uint8(g), B: uint8(b)}, true
}
