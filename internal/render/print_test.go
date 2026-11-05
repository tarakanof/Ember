package render

import (
	"bytes"
	"flag"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updatePrintGolden = flag.Bool("update", false, "rewrite the print golden frames")

func printFrameText(f *Frame) string {
	legend := map[RGB]byte{}
	var next byte = 'a'
	var sb strings.Builder
	var order []RGB
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			if !f.Dirty[y][x] || f.Pixels[y][x] == (RGB{}) {
				sb.WriteByte('.')
				continue
			}
			c := f.Pixels[y][x]
			ch, ok := legend[c]
			if !ok {
				ch = next
				next++
				legend[c] = ch
				order = append(order, c)
			}
			sb.WriteByte(ch)
		}
		sb.WriteByte('\n')
	}
	if n := f.Native; n != nil {
		sb.WriteString("native " + n.Text + "\n")
	}
	for _, c := range order {
		sb.WriteString(string(legend[c]) + "=" + hexOf(c) + "\n")
	}
	return sb.String()
}

func printSample(phase string) PrintView {
	return PrintView{Phase: phase, Progress: 42, HaveLeft: true, LeftSec: 102 * 60, ReadyAt: "17:30",
		Nozzle: 240, NozzleTarget: 240, Bed: 85, BedTarget: 85}
}

func printGoldenCases() map[string]Frame {
	heating := printSample(PrintPhaseHeating)
	heating.Progress, heating.Nozzle, heating.Bed = 0, 160, 50
	soon := printSample(PrintPhasePrinting)
	soon.ChangeSoon = true
	noEst := printSample(PrintPhasePrinting)
	noEst.HaveLeft, noEst.ReadyAt = false, ""
	return map[string]Frame{
		"heating":        PrintTileFrame(heating, PrintCardTime),
		"printing_time":  PrintTileFrame(printSample(PrintPhasePrinting), PrintCardTime),
		"printing_ready": PrintTileFrame(printSample(PrintPhasePrinting), PrintCardReady),
		"printing_pct":   PrintTileFrame(printSample(PrintPhasePrinting), PrintCardPct),
		"no_estimate":    PrintTileFrame(noEst, PrintCardTime),
		"colour_change":  PrintTileFrame(soon, PrintCardTime),
		"preparing":      PrintTileFrame(printSample(PrintPhasePreparing), PrintCardTime),
		"finishing":      PrintTileFrame(printSample(PrintPhaseFinishing), PrintCardTime),
		"paused":         PrintTileFrame(printSample(PrintPhasePaused), PrintCardTime),
		"attention":      PrintTileFrame(printSample(PrintPhaseAttention), PrintCardTime),
		"finished":       PrintTileFrame(printSample(PrintPhaseFinished), PrintCardTime),
		"stopped":        PrintTileFrame(printSample(PrintPhaseStopped), PrintCardTime),
	}
}

func TestPrintGoldenFrames(t *testing.T) {
	for name, f := range printGoldenCases() {
		got := printFrameText(&f)
		path := filepath.Join("testdata", "print", name+".txt")
		if *updatePrintGolden {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run go test ./internal/render -update)", name, err)
		}
		if string(want) != got {
			t.Errorf("%s drifted (run with -update if intended)\ngot:\n%s\nwant:\n%s", name, got, want)
		}
	}
}

func TestPrintIconMovesLikeTheCoreOne(t *testing.T) {
	cases := []struct{ progress, bedRow int }{{0, 3}, {1, 4}, {33, 4}, {34, 5}, {66, 5}, {67, 6}, {100, 6}}
	for _, tc := range cases {
		v := PrintView{Phase: PrintPhasePrinting, Progress: tc.progress}
		var f Frame
		paintPrintIconFor(&f, v)
		if got := printBedRows(v) + 3; got != tc.bedRow {
			t.Errorf("progress %d: bed row %d, want %d", tc.progress, got, tc.bedRow)
		}
		for x := 1; x <= 6; x++ {
			if f.Pixels[tc.bedRow][x] != printBed {
				t.Errorf("progress %d: col %d row %d is not the bed", tc.progress, x, tc.bedRow)
			}
		}
		for y := 3; y < tc.bedRow; y++ {
			for x := 2; x <= 5; x++ {
				if !f.Dirty[y][x] {
					t.Errorf("progress %d: part missing at col %d row %d", tc.progress, x, y)
				}
			}
		}
		for x := 0; x < iconW; x++ {
			for y := tc.bedRow + 1; y < 7; y++ {
				if f.Dirty[y][x] && x != 0 && x != 7 {
					t.Errorf("progress %d: pixel under the bed at col %d row %d", tc.progress, x, y)
				}
			}
		}
		if !f.Dirty[1][printRestHeadX] || f.Pixels[2][printRestHeadX] != colorRunning {
			t.Errorf("progress %d: nozzle moved off rows 1-2", tc.progress)
		}
	}
}

func TestPrintSweepGIFKeepsTheNozzleAtTheTop(t *testing.T) {
	icons, err := PrintIcons()
	if err != nil {
		t.Fatal(err)
	}
	if len(icons) != printBedLevels+1 {
		t.Fatalf("%d icons, want %d", len(icons), printBedLevels+1)
	}
	for h := 1; h <= printBedLevels; h++ {
		name := PrintIconName(h)
		g, err := gif.DecodeAll(bytes.NewReader(icons[name]))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(g.Image) != len(printSweepX) {
			t.Fatalf("%s: %d frames, want %d", name, len(g.Image), len(printSweepX))
		}
		heads := map[int]bool{}
		for i, img := range g.Image {
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					lit := img.ColorIndexAt(x, y) != 0
					if y >= 3+h+1 && y < 7 && x > 0 && x < 7 && lit {
						t.Errorf("%s frame %d: lit pixel under the bed at %d,%d", name, i, x, y)
					}
					if y == 2 && lit {
						heads[x] = true
					}
				}
			}
		}
		if len(heads) < 4 {
			t.Errorf("%s: nozzle visits %d columns, want a sweep", name, len(heads))
		}
	}
}

func TestPrintPayloadPicksTheSweepIconOnlyWhenArmed(t *testing.T) {
	v := printSample(PrintPhasePrinting)
	static := PrintPayload(v, PrintCardTime, false, 60)
	if _, ok := static["icon"]; ok {
		t.Error("unarmed payload names an icon the clock does not hold")
	}
	if op := static["draw"].([]any)[0].([]any); op[1] != 0 || op[3] != panelW {
		t.Errorf("static draw op = %v, want the whole panel", op[:5])
	}
	armed := PrintPayload(v, PrintCardTime, true, 60)
	if armed["icon"] != PrintIconName(printBedRows(v)) {
		t.Errorf("icon = %v, want %s", armed["icon"], PrintIconName(printBedRows(v)))
	}
	if op := armed["draw"].([]any)[0].([]any); op[1] != iconW || op[3] != panelW-iconW {
		t.Errorf("armed draw op = %v, want the area right of the icon", op[:5])
	}
	paused := PrintPayload(printSample(PrintPhasePaused), PrintCardTime, true, 60)
	if _, ok := paused["icon"]; ok {
		t.Error("a paused print must not sweep")
	}
	if static["durationMs"] != 3000 {
		t.Errorf("cards dwell %v ms, want 3000", static["durationMs"])
	}
}

func TestPrintAttentionBlinksItsLabelNatively(t *testing.T) {
	p := PrintPayload(printSample(PrintPhaseAttention), PrintCardTime, true, 60)
	if p["text"] != "PRINT" || p["textBlinkMs"] != printBlinkMs {
		t.Errorf("attention payload text=%v blink=%v", p["text"], p["textBlinkMs"])
	}
	if errs := CheckNGPayload(p, false); len(errs) > 0 {
		t.Errorf("not NG's schema: %v", errs)
	}
}

func TestPrintPopupsFitNGSchema(t *testing.T) {
	for name, p := range map[string]map[string]any{
		"done":      PrintDonePopupPayload("2:14", 10, false),
		"attention": PrintAttentionPopupPayload(120),
		"error":     PrintErrorPopupPayload(30),
	} {
		if errs := CheckNGPayload(p, true); len(errs) > 0 {
			t.Errorf("%s: %v", name, errs)
		}
	}
	if got := PrintDonePopupPayload("2:14", 10, false)["text"]; got != "DONE 2:14" {
		t.Errorf("done text = %v", got)
	}
	if got := PrintDonePopupPayload("", 10, false)["text"]; got != "DONE" {
		t.Errorf("done text without a total = %v", got)
	}
}

func TestPrintBurstGIFPlaysOnceAndEndsOnTheDoneIcon(t *testing.T) {
	icons, err := PrintIcons()
	if err != nil {
		t.Fatal(err)
	}
	name := PrintDoneIconName()
	g, err := gif.DecodeAll(bytes.NewReader(icons[name]))
	if err != nil {
		t.Fatal(err)
	}
	if d := g.Delay[len(g.Delay)-1]; d != printBurstHoldCs {
		t.Errorf("last frame delay = %d, want %d", d, printBurstHoldCs)
	}
	if g.LoopCount != -1 {
		t.Errorf("LoopCount = %d, want -1 (play once)", g.LoopCount)
	}
	if len(g.Image) < 2 {
		t.Fatalf("%d frames", len(g.Image))
	}
	for i, img := range g.Image {
		if b := img.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
			t.Errorf("frame %d is %v, want 8x8", i, b)
		}
	}
	want := printPopupIcon(PrintPhaseFinished)
	differs := false
	for _, img := range g.Image[:len(g.Image)-1] {
		for y := 0; y < 8 && !differs; y++ {
			for x := 0; x < 8; x++ {
				r, gg, b, _ := img.At(x, y).RGBA()
				if int(r>>8)<<16|int(gg>>8)<<8|int(b>>8) != want[y*8+x] {
					differs = true
					break
				}
			}
		}
	}
	if !differs {
		t.Error("no earlier frame differs from the final icon: there is no burst")
	}
	last := g.Image[len(g.Image)-1]
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			r, gg, b, _ := last.At(x, y).RGBA()
			got := int(r>>8)<<16 | int(gg>>8)<<8 | int(b>>8)
			if got != want[y*8+x] {
				t.Fatalf("last frame (%d,%d) = %06x, want %06x", x, y, got, want[y*8+x])
			}
		}
	}
}

func TestPrintDonePopupUsesTheBurstIcon(t *testing.T) {
	p := PrintDonePopupPayload("2:14", 10, true)
	if p["icon"] != PrintDoneIconName() || p["draw"] != nil {
		t.Errorf("popup = %v", p)
	}
	if errs := CheckNGPayload(p, true); len(errs) > 0 {
		t.Error(errs)
	}
}
