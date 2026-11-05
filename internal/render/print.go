package render

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"strings"
	"sync"
)

const (
	PrintPhaseHeating   = "heating"
	PrintPhasePreparing = "preparing"
	PrintPhasePrinting  = "printing"
	PrintPhaseFinishing = "finishing"
	PrintPhasePaused    = "paused"
	PrintPhaseAttention = "attention"
	PrintPhaseFinished  = "finished"
	PrintPhaseStopped   = "stopped"
	PrintPhaseError     = "error"

	PrintCardTime  = 0
	PrintCardReady = 1
	PrintCardPct   = 2

	PrintIconPrefix = "ember_print_"

	printDwellSeconds = 3
	printBedLevels    = 3
	printBlinkMs      = 500
)

type PrintView struct {
	Phase        string
	Progress     int
	LeftSec      int
	HaveLeft     bool
	ReadyAt      string
	ChangeSoon   bool
	Nozzle       int
	NozzleTarget int
	Bed          int
	BedTarget    int
}

var (
	printBody  = RGB{0xff, 0x8a, 0x3d}
	printFil   = RGB{0xff, 0x6a, 0x2a}
	printHeat  = RGB{0xff, 0x5a, 0x3a}
	printGrey  = RGB{0x9a, 0xa3, 0xad}
	printFrame = RGB{0x8a, 0x8a, 0x8a}
	printBase  = RGB{0x5a, 0x5a, 0x5a}
	printHead  = RGB{0xbb, 0xbb, 0xbb}
	printBed   = RGB{0x55, 0x55, 0x55}
	printCool  = RGB{0xbf, 0xe3, 0xff}
	printGlass = RGB{0x77, 0x77, 0x77}
	printTrack = RGB{0x2a, 0x1a, 0x10}
	printDimAm = RGB{0x2a, 0x21, 0x10}
	printDimGr = RGB{0x1a, 0x1a, 0x1a}
	printDimHt = RGB{0x2a, 0x12, 0x10}
	printOff   = RGB{0x33, 0x33, 0x33}
	printStopB = RGB{0x55, 0x55, 0x55}
)

var printHourglass = []string{"XXX", "X.X", ".X.", "X.X", "XXX"}

func mixRGB(a, b RGB, k float64) RGB {
	if k < 0 {
		k = 0
	}
	if k > 1 {
		k = 1
	}
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*k + 0.5) }
	return RGB{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B)}
}

func scaleRGB(c RGB, k float64) RGB {
	return RGB{uint8(float64(c.R) * k), uint8(float64(c.G) * k), uint8(float64(c.B) * k)}
}

func printHeatK(v PrintView) float64 {
	frac := func(cur, target int) float64 {
		if target <= 0 {
			return 0
		}
		k := float64(cur) / float64(target)
		if k > 1 {
			return 1
		}
		if k < 0 {
			return 0
		}
		return k
	}
	return 0.6*frac(v.Nozzle, v.NozzleTarget) + 0.4*frac(v.Bed, v.BedTarget)
}

func printBedRows(v PrintView) int {
	switch v.Phase {
	case PrintPhaseHeating, PrintPhasePreparing:
		return 0
	case PrintPhaseFinished:
		return printBedLevels
	}
	p := v.Progress
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return (p*printBedLevels + 99) / 100
}

func printNozzleColor(phase string) RGB {
	switch phase {
	case PrintPhaseHeating, PrintPhaseError:
		return printHeat
	case PrintPhasePaused, PrintPhaseAttention:
		return colorWaiting
	case PrintPhaseFinished:
		return colorDone
	case PrintPhaseStopped:
		return printGrey
	}
	return colorRunning
}

func printAccent(phase string) RGB {
	switch phase {
	case PrintPhaseHeating:
		return printHeat
	case PrintPhasePaused, PrintPhaseAttention:
		return colorWaiting
	case PrintPhaseFinished:
		return colorDone
	case PrintPhaseStopped:
		return printGrey
	}
	return printBody
}

func paintPrintIcon(f *Frame, h, headX int, nozzle RGB, bedColor RGB) {
	paintRow(f, 0, 7, 0, printBody)
	for y := 1; y < 7; y++ {
		paintCell(f, 0, y, printFrame)
		paintCell(f, 7, y, printFrame)
	}
	paintRow(f, 0, 7, 7, printBase)
	for j := 0; j < h; j++ {
		c := printFil
		if j > 0 {
			c = scaleRGB(printFil, 0.7)
		}
		paintRow(f, 2, 5, 3+j, c)
	}
	paintRow(f, 1, 6, 3+h, bedColor)
	paintRow(f, headX-1, headX+1, 1, printHead)
	paintCell(f, headX, 2, nozzle)
}

const printRestHeadX = 5

func paintPrintIconFor(f *Frame, v PrintView) {
	bed := printBed
	if v.Phase == PrintPhaseHeating {
		bed = mixRGB(RGB{0x3a, 0x3a, 0x3a}, printHeat, printHeatK(v))
	}
	paintPrintIcon(f, printBedRows(v), printRestHeadX, printNozzleColor(v.Phase), bed)
}

func printIconPixels(v PrintView) []int {
	var f Frame
	paintPrintIconFor(&f, v)
	return framePixelsRect(&f, 0, 0, 8, 8)
}

var printSweepX = []int{2, 3, 4, 5, 5, 4, 3, 2}

const printSweepDelayCs = 25

func printIconSet() (map[string][]byte, error) {
	out := make(map[string][]byte, printBedLevels)
	for h := 1; h <= printBedLevels; h++ {
		data, err := printSweepGIF(h)
		if err != nil {
			return nil, err
		}
		out[fmt.Sprintf("%sh%d_%08x", PrintIconPrefix, h, crc32.ChecksumIEEE(data))] = data
	}
	burst, err := printBurstGIF()
	if err != nil {
		return nil, err
	}
	out[fmt.Sprintf("%sdone_%08x", PrintIconPrefix, crc32.ChecksumIEEE(burst))] = burst
	return out, nil
}

var printIcons = sync.OnceValues(printIconSet)

func PrintIcons() (map[string][]byte, error) { return printIcons() }

func PrintIconName(h int) string {
	if h < 1 {
		h = 1
	}
	if h > printBedLevels {
		h = printBedLevels
	}
	icons, _ := PrintIcons()
	prefix := fmt.Sprintf("%sh%d_", PrintIconPrefix, h)
	for name := range icons {
		if strings.HasPrefix(name, prefix) {
			return name
		}
	}
	return ""
}

func PrintDoneIconName() string {
	icons, _ := PrintIcons()
	prefix := PrintIconPrefix + "done_"
	for name := range icons {
		if strings.HasPrefix(name, prefix) {
			return name
		}
	}
	return ""
}

func printSweepGIF(h int) ([]byte, error) {
	var frames []Frame
	for _, hx := range printSweepX {
		var f Frame
		paintPrintIcon(&f, h, hx, colorRunning, printBed)
		frames = append(frames, f)
	}
	return encodePrintGIF(frames, printSweepDelayCs, 0, false)
}

const (
	printBurstDelayCs = 10
	printBurstHoldCs  = 40
	printBurstRadii   = 5
)

func printBurstGIF() ([]byte, error) {
	var frames []Frame
	for r := 2; r <= printBurstRadii; r++ {
		var f Frame
		paintPrintIconFor(&f, PrintView{Phase: PrintPhaseFinished, Progress: 100})
		k := 1 - float64(r-2)/float64(printBurstRadii-1)
		spark := mixRGB(colorWhite, colorDone, 1-k)
		for _, d := range [][2]int{{r, 0}, {-r, 0}, {0, r}, {0, -r}, {r - 1, r - 1}, {1 - r, r - 1}, {r - 1, 1 - r}, {1 - r, 1 - r}} {
			x, y := 4+d[0], 4+d[1]
			if x >= 0 && x < iconW && y >= 0 && y < 8 {
				paintCell(&f, x, y, spark)
			}
		}
		frames = append(frames, f)
	}
	var last Frame
	paintPrintIconFor(&last, PrintView{Phase: PrintPhaseFinished, Progress: 100})
	frames = append(frames, last)
	return encodePrintGIF(frames, printBurstDelayCs, printBurstHoldCs, true)
}

func encodePrintGIF(frames []Frame, delayCs, lastDelayCs int, once bool) ([]byte, error) {
	palette := color.Palette{color.Black}
	index := map[RGB]uint8{}
	for _, f := range frames {
		for y := 0; y < 8; y++ {
			for x := 0; x < iconW; x++ {
				c := f.Pixels[y][x]
				if !f.Dirty[y][x] {
					continue
				}
				if _, ok := index[c]; !ok {
					index[c] = uint8(len(palette))
					palette = append(palette, color.RGBA{c.R, c.G, c.B, 0xff})
				}
			}
		}
	}
	g := &gif.GIF{}
	if once {
		g.LoopCount = -1
	}
	for i, f := range frames {
		img := image.NewPaletted(image.Rect(0, 0, iconW, 8), palette)
		for y := 0; y < 8; y++ {
			for x := 0; x < iconW; x++ {
				if f.Dirty[y][x] {
					img.SetColorIndex(x, y, index[f.Pixels[y][x]])
				}
			}
		}
		g.Image = append(g.Image, img)
		d := delayCs
		if lastDelayCs > 0 && i == len(frames)-1 {
			d = lastDelayCs
		}
		g.Delay = append(g.Delay, d)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func PrintSweeps(phase string) bool { return phase == PrintPhasePrinting }

func drawPrintText(f *Frame, text string, x, y int, c RGB) int {
	for _, ch := range text {
		g := glyph(ch)
		if g == nil {
			x += 4
			continue
		}
		paintBitmap(f, x, y, g, c)
		x += len(g[0]) + 1
	}
	return x
}

func printHM(sec int) string {
	if sec < 0 {
		sec = 0
	}
	m := (sec + 30) / 60
	return fmt.Sprintf("%d:%02d", m/60, m%60)
}

func PrintDuration(sec int) string { return printHM(sec) }

func drawPrintBar(f *Frame, progress int, c, track RGB) {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	fill := float64(progress) / 100 * float64(barW)
	for i := 0; i < barW; i++ {
		k := fill - float64(i)
		switch {
		case k >= 1:
			paintCell(f, barX0+i, barRow, c)
		case k > 0:
			paintCell(f, barX0+i, barRow, mixRGB(track, c, k))
		default:
			paintCell(f, barX0+i, barRow, track)
		}
	}
}

func drawPrintChangeBar(f *Frame, progress int) {
	drawPrintBar(f, progress, printBody, printTrack)
	from := (progress*barW + 99) / 100
	for i := from; i < barW; i++ {
		paintCell(f, barX0+i, barRow, colorWaiting)
	}
}

func PrintTileFrame(v PrintView, card int) Frame {
	var f Frame
	paintPrintIconFor(&f, v)
	switch v.Phase {
	case PrintPhaseHeating:
		drawPrintText(&f, fmt.Sprintf("%d°", v.Nozzle), contentX, textRow, mixRGB(colorWhite, printHeat, printHeatK(v)*0.7))
		drawPrintBar(&f, int(printHeatK(v)*100+0.5), printHeat, printDimHt)
	case PrintPhaseAttention:
		label := "PRINT"
		f.Native = &NativeText{Text: label, X: contentX, W: textWidthPx(label), Color: colorWaiting}
		drawPrintBar(&f, v.Progress, colorWaiting, printDimAm)
	case PrintPhasePaused:
		drawPrintText(&f, "PAUSE", contentX, textRow, colorWaiting)
		drawPrintBar(&f, v.Progress, colorWaiting, printDimAm)
	case PrintPhaseFinished:
		drawPrintText(&f, "DONE", contentX+1, textRow, colorDone)
		drawPrintBar(&f, 100, colorDone, printDimGr)
	case PrintPhaseStopped:
		drawPrintText(&f, "STOP", contentX+1, textRow, printGrey)
		drawPrintBar(&f, v.Progress, printStopB, printDimGr)
	case PrintPhaseFinishing:
		drawPrintText(&f, "COOL", contentX+1, textRow, printCool)
		drawPrintBar(&f, 100, printBody, printTrack)
	default:
		drawPrintCard(&f, v, card)
		if v.ChangeSoon {
			drawPrintChangeBar(&f, v.Progress)
		} else {
			drawPrintBar(&f, v.Progress, printBody, printTrack)
		}
	}
	return f
}

func drawPrintClock(f *Frame, text string, x, y int, c RGB) int {
	for _, ch := range text {
		if ch == ':' {
			paintCell(f, x, y+1, c)
			paintCell(f, x, y+3, c)
			x += 2
			continue
		}
		if g := glyph(ch); g != nil {
			paintBitmap(f, x, y, g, c)
		}
		x += 4
	}
	return x
}

func drawPrintCard(f *Frame, v PrintView, card int) {
	switch {
	case card == PrintCardTime && v.HaveLeft:
		drawPrintClock(f, printHM(v.LeftSec), contentX, textRow, colorWhite)
		paintBitmap(f, 27, textRow, printHourglass, printGlass)
	case card == PrintCardReady && v.ReadyAt != "":
		drawPrintClock(f, v.ReadyAt, contentX, textRow, printBody)
	default:
		drawPrintText(f, fmt.Sprintf("%d%%", v.Progress), contentX, textRow, colorWhite)
	}
}

func PrintPayload(v PrintView, card int, sweepIcon bool, lifetimeSeconds int) map[string]any {
	f := PrintTileFrame(v, card)
	p := map[string]any{
		"lifetimeMs": msOf(lifetimeSeconds),
		"durationMs": msOf(printDwellSeconds),
	}
	if sweepIcon && PrintSweeps(v.Phase) {
		if name := PrintIconName(printBedRows(v)); name != "" {
			p["icon"] = name
			p["draw"] = []any{bitmapOp(iconW, 0, panelW-iconW, 8, framePixelsRect(&f, iconW, 0, panelW-iconW, 8))}
			return p
		}
	}
	p["draw"] = []any{bitmapOp(0, 0, 32, 8, framePixels(&f))}
	if n := f.Native; n != nil && n.Text != "" {
		p["draw"] = drawOpsAround(&f, n)
		p["text"] = n.Text
		p["textColor"] = hexOf(n.Color)
		p["textOffsetX"] = n.X
		p["textCenter"] = false
		p["scroll"] = scrollStaticWhenFits()
		p["textBlinkMs"] = printBlinkMs
	}
	return p
}

func printPopupIcon(phase string) []int {
	return printIconPixels(PrintView{Phase: phase, Progress: 100})
}

func printPopup(text string, c RGB, phase string, durationSec int) map[string]any {
	return pinText(map[string]any{
		"text":        text,
		"textColor":   hexOf(c),
		"durationMs":  msOf(durationSec),
		"wakeup":      true,
		"draw":        []any{iconOp(printPopupIcon(phase))},
		"textCenter":  false,
		"textOffsetX": 9,
	})
}

func PrintDonePopupPayload(total string, durationSec int, burstIcon bool) map[string]any {
	text := "DONE"
	if total != "" {
		text += " " + total
	}
	p := readOnce(printPopup(text, colorDone, PrintPhaseFinished, durationSec))
	if burstIcon {
		if name := PrintDoneIconName(); name != "" {
			p["icon"] = name
			delete(p, "draw")
			delete(p, "textOffsetX")
		}
	}
	return p
}

func PrintAttentionPopupPayload(durationSec int) map[string]any {
	return printPopup("CHECK PRINTER", colorWaiting, PrintPhaseAttention, durationSec)
}

func PrintErrorPopupPayload(durationSec int) map[string]any {
	return readOnce(printPopup("PRINTER ERROR", printHeat, PrintPhaseError, durationSec))
}
