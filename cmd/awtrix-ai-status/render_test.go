package main

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestFrameAndPainters(t *testing.T) {
	f := &Frame{}

	// paintCell sets a single pixel.
	paintCell(f, 5, 2, RGB{0x12, 0x34, 0x56})
	if !f.Dirty[2][5] {
		t.Fatalf("paintCell(5,2): Dirty[2][5] = false, want true")
	}
	if got := f.Pixels[2][5]; got != (RGB{0x12, 0x34, 0x56}) {
		t.Fatalf("paintCell(5,2) color = %v, want #123456", got)
	}

	// paintRow sets a horizontal run inclusive on both ends.
	paintRow(f, 10, 13, 7, RGB{0xff, 0x00, 0x00})
	for x := 10; x <= 13; x++ {
		if !f.Dirty[7][x] {
			t.Fatalf("paintRow: Dirty[7][%d] = false, want true", x)
		}
	}
	if f.Dirty[7][9] || f.Dirty[7][14] {
		t.Fatalf("paintRow leaked outside [10..13]")
	}

	// paintBitmap paints lit pixels of a sprite at an offset.
	sprite := []string{
		".X.",
		"XXX",
		".X.",
	}
	paintBitmap(f, 20, 0, sprite, RGB{0x00, 0xff, 0x00})
	want := map[[2]int]bool{
		{20, 0}: false, {21, 0}: true, {22, 0}: false,
		{20, 1}: true, {21, 1}: true, {22, 1}: true,
		{20, 2}: false, {21, 2}: true, {22, 2}: false,
	}
	for pos, lit := range want {
		got := f.Dirty[pos[1]][pos[0]]
		if got != lit {
			t.Fatalf("paintBitmap: Dirty[%d][%d] = %v, want %v", pos[1], pos[0], got, lit)
		}
	}

	// Out-of-bounds writes are silent no-ops, not panics.
	paintCell(f, 32, 0, RGB{1, 2, 3})
	paintCell(f, 0, 8, RGB{1, 2, 3})
	paintCell(f, -1, -1, RGB{1, 2, 3})
}

func TestFontLookup(t *testing.T) {
	for _, ch := range "0123456789/+" {
		g := glyph(ch)
		if g == nil {
			t.Fatalf("glyph(%q) = nil, want a sprite", ch)
		}
		if len(g) != 5 {
			t.Fatalf("glyph(%q) height = %d, want 5", ch, len(g))
		}
		for i, row := range g {
			if len(row) != 3 {
				t.Fatalf("glyph(%q) row %d width = %d, want 3", ch, i, len(row))
			}
		}
	}
	if glyph('Z') != nil {
		t.Fatalf("glyph('Z') = non-nil, want nil for unsupported rune")
	}
}

func TestDrawDigits(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		startX    int
		wantLit   map[[2]int]bool
		wantClear [][2]int
	}{
		{
			name:   "single 1 at col 12",
			text:   "1",
			startX: 12,
			wantLit: map[[2]int]bool{
				{13, 1}: true,
				{12, 2}: true, {13, 2}: true,
				{13, 3}: true,
				{13, 4}: true,
				{12, 5}: true, {13, 5}: true, {14, 5}: true,
			},
		},
		{
			name:   "1/3 at col 12",
			text:   "1/3",
			startX: 12,
			wantLit: map[[2]int]bool{
				{13, 1}: true,
				{18, 1}: true, {18, 2}: true, {17, 3}: true, {16, 4}: true,
				{20, 1}: true, {21, 1}: true, {22, 1}: true,
			},
			wantClear: [][2]int{
				{15, 3},
				{19, 3},
				{23, 1},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &Frame{}
			drawDigits(f, tc.text, tc.startX, 1, RGB{0xff, 0xff, 0xff})
			for pos, want := range tc.wantLit {
				got := f.Dirty[pos[1]][pos[0]]
				if got != want {
					t.Errorf("[%d,%d] lit = %v, want %v", pos[0], pos[1], got, want)
				}
			}
			for _, pos := range tc.wantClear {
				if f.Dirty[pos[1]][pos[0]] {
					t.Errorf("[%d,%d] lit = true, want false (clear)", pos[0], pos[1])
				}
			}
		})
	}
}

func TestDrawRobotNormal(t *testing.T) {
	f := &Frame{}
	drawRobot(f, Session{State: "running"}, RGB{0x2e, 0xe8, 0x5e})

	mustLit := [][2]int{
		{1, 1}, {2, 1}, {3, 1}, {7, 1}, {8, 1},
		{0, 4}, {9, 4},
		{1, 6}, {3, 6}, {6, 6}, {8, 6},
	}
	for _, p := range mustLit {
		if !f.Dirty[p[1]][p[0]] {
			t.Errorf("normal: [%d,%d] lit = false, want true", p[0], p[1])
		}
	}

	mustDark := [][2]int{{2, 2}, {2, 3}, {7, 2}, {7, 3}}
	for _, p := range mustDark {
		if f.Dirty[p[1]][p[0]] {
			t.Errorf("normal: [%d,%d] lit = true, want false (eye hole)", p[0], p[1])
		}
	}
}

func TestDrawRobotError(t *testing.T) {
	f := &Frame{}
	drawRobot(f, Session{State: "error"}, RGB{0xff, 0x3a, 0x3a})

	holes := [][2]int{{2, 2}, {3, 3}, {2, 4}, {7, 2}, {6, 3}, {7, 4}}
	for _, p := range holes {
		if f.Dirty[p[1]][p[0]] {
			t.Errorf("error: [%d,%d] lit = true, want false (chevron hole)", p[0], p[1])
		}
	}

	for _, x := range []int{0, 9} {
		if !f.Dirty[4][x] {
			t.Errorf("error: arm protrusion at [%d,4] lit = false, want true", x)
		}
	}
}

func intPtr(v int) *int { return &v }

// litInterior counts lit pixels in the glass interior (cols 26-29, rows 1-4).
func litInterior(f *Frame) int {
	n := 0
	for y := 1; y <= 4; y++ {
		for x := 26; x <= 29; x++ {
			if f.Dirty[y][x] {
				n++
			}
		}
	}
	return n
}

func TestDrawGlass(t *testing.T) {
	fill := RGB{0x2e, 0xe8, 0x5e}

	// Absent pct → nothing drawn (no outline, no fill).
	f := &Frame{}
	drawGlass(f, nil, fill)
	if f.Dirty[1][25] || litInterior(f) != 0 {
		t.Errorf("absent pct: drew something")
	}

	// Proportional pixel fill over the 16 interior pixels: round(pct/100*16).
	// Outline is always present for a non-nil pct.
	counts := []struct{ pct, want int }{
		{0, 0}, {25, 4}, {50, 8}, {75, 12}, {100, 16},
		{73, 12}, {99, 16}, // 73 and 99 are now distinguishable (12 vs 16)
		{6, 1},             // ~6% per pixel → first pixel
	}
	for _, c := range counts {
		f := &Frame{}
		drawGlass(f, intPtr(c.pct), fill)
		if !f.Dirty[1][25] || !f.Dirty[1][30] || !f.Dirty[5][27] {
			t.Errorf("%d%%: outline missing", c.pct)
		}
		if got := litInterior(f); got != c.want {
			t.Errorf("%d%%: interior lit = %d, want %d", c.pct, got, c.want)
		}
	}

	// Center-out partial row: 54% → 9px → rows 4,3 full + only center col 27 in row 2.
	f = &Frame{}
	drawGlass(f, intPtr(54), fill)
	for x := 26; x <= 29; x++ {
		if !f.Dirty[4][x] || !f.Dirty[3][x] {
			t.Errorf("54%%: bottom two rows should be full (col %d)", x)
		}
	}
	if !f.Dirty[2][27] {
		t.Error("54%: center col 27 of the partial row should be lit")
	}
	for _, x := range []int{26, 28, 29} {
		if f.Dirty[2][x] {
			t.Errorf("54%%: partial row fills center-out; col %d should still be dark", x)
		}
	}
	if litInterior(f) != 9 {
		t.Errorf("54%%: interior lit = %d, want 9", litInterior(f))
	}

	// 60% → 10px → partial row has the center PAIR (27,28); outer (26,29) dark.
	f = &Frame{}
	drawGlass(f, intPtr(60), fill)
	if !f.Dirty[2][27] || !f.Dirty[2][28] || f.Dirty[2][26] || f.Dirty[2][29] {
		t.Error("60%: partial row should be center pair 27,28 only")
	}
}

func TestRenderForCoord_NoActive_ReturnsNil(t *testing.T) {
	if got := RenderForCoord(Snapshot{}, "", cardXY, false, 30); got != nil {
		t.Fatalf("empty snapshot: got %v, want nil", got)
	}
}

func TestRenderForCoord_PointerMissing_PicksFirst(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "c", State: "running", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "missing/key/nope", cardXY, false, 30)
	if payload == nil {
		t.Fatal("expected non-nil payload for single running session")
	}
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	if pixels[3*32+21] == 0 {
		t.Errorf("expected '1/1' second digit lit at (21,3)")
	}
}

func TestRenderForCoord_TwoActive_HonorsPointer(t *testing.T) {
	purple := "#aa66ff"
	green := "#2ee85e"
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", SourceColor: &purple, UpdatedAt: time.Now()},
		{Source: "a", Tool: "b", Session: "s2", State: "running", SourceColor: &green, UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/s2", cardXY, false, 30)
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// First digit '1' first sprite row col 1 → matrix (13, 1). Should be green.
	if got, want := pixels[1*32+13], 0x2ee85e; got != want {
		t.Errorf("digit colour at (13,1) = %#06x, want %#06x (s2 SourceColor)", got, want)
	}
}

func TestRenderForCoord_LockedAttention_EmitsBlinkText(t *testing.T) {
	tests := []struct {
		state     string
		wantLabel string
		wantColor string
	}{
		{"waiting", "WAIT", "#FFC14D"},
		{"error", "ERR", "#FF3A3A"},
	}
	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			snap := Snapshot{Sessions: []Session{
				{Source: "a", Tool: "b", Session: "w", State: tc.state, UpdatedAt: time.Now()},
			}}
			payload := RenderForCoord(snap, "a/b/w", cardXY, true, 30)
			assertBlinkText(t, payload, tc.wantLabel, tc.wantColor)
		})
	}
}

// TestRenderForCoord_Locked_PointerWinsOverRotation proves that when
// multiple sessions are active, the locked pointer's state — not the
// sort-order default — drives the attention payload. Without this,
// the single-session test above could pass even if the code accidentally
// always picked keys[0].
func TestRenderForCoord_Locked_PointerWinsOverRotation(t *testing.T) {
	now := time.Now()
	snap := Snapshot{Sessions: []Session{
		// Sort order will put s1 (running) ahead of s2 (error) because
		// state priority puts error first… so to make this a real test
		// we want pointer to select a session that is NOT keys[0].
		{Source: "a", Tool: "b", Session: "s1", State: "error", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "s2", State: "waiting", UpdatedAt: now},
	}}
	// Pointer locked on s2 (waiting) even though s1 (error) sorts first
	// because state priority puts error ahead of waiting.
	payload := RenderForCoord(snap, "a/b/s2", cardXY, true, 30)
	assertBlinkText(t, payload, "WAIT", "#FFC14D")
}

// TestRenderForCoord_LockedAttention_PixelGeometry asserts the locked
// payload uses the full 10-wide rotation sprite — both arm protrusions
// (cols 0 & 9 on row 4), both eyes (cols 2 & 7 on rows 2-3), and all
// four legs (cols 1, 3, 6, 8 on row 6). Guards against silent regression
// to the 8-wide cramped variant.
func TestRenderForCoord_LockedAttention_PixelGeometry(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "w", State: "waiting", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/w", cardXY, true, 30)
	draw := payload["draw"].([]any)
	db := draw[0].(map[string]any)["db"].([]any)
	if db[2] != 10 {
		t.Fatalf("locked sprite width = %v, want 10 (rotation-sprite parity)", db[2])
	}
	pixels, ok := db[4].([]int)
	if !ok || len(pixels) != 80 {
		t.Fatalf("locked pixel array = %v, want []int of length 80 (10 cols × 8 rows)", db[4])
	}
	// Helper: pixel at (col, row) in the 10-wide layout.
	at := func(x, y int) int { return pixels[y*10+x] }
	// Row 4 is the arms row — both protrusions at col 0 and col 9 lit.
	if at(0, 4) == 0 {
		t.Errorf("row 4 col 0 (left arm protrusion) is dark, want lit")
	}
	if at(9, 4) == 0 {
		t.Errorf("row 4 col 9 (right arm protrusion) is dark, want lit — regression to 8-wide")
	}
	// Row 2-3 are the eyes — col 2 and col 7 must be DARK (the eye holes).
	if at(2, 2) != 0 {
		t.Errorf("row 2 col 2 (left eye hole) is lit, want dark")
	}
	if at(7, 2) != 0 {
		t.Errorf("row 2 col 7 (right eye hole) is lit, want dark — regression to 8-wide")
	}
	// Row 6 is the legs row — all 4 legs at cols 1, 3, 6, 8 must be lit.
	for _, x := range []int{1, 3, 6, 8} {
		if at(x, 6) == 0 {
			t.Errorf("row 6 col %d (leg) is dark, want lit — regression to 2-leg 8-wide variant", x)
		}
	}
	// And cols 0, 2, 4, 5, 7, 9 on the legs row must be DARK.
	for _, x := range []int{0, 2, 4, 5, 7, 9} {
		if at(x, 6) != 0 {
			t.Errorf("row 6 col %d is lit, want dark (only 4 specific leg pixels expected)", x)
		}
	}
}

func assertBlinkText(t *testing.T, payload map[string]any, wantLabel, wantColor string) {
	t.Helper()
	draw, ok := payload["draw"].([]any)
	if !ok || len(draw) != 1 {
		t.Fatalf("locked payload draw[] = %v, want exactly 1 entry (firmware rejects multi-frame draws)", payload["draw"])
	}
	db, ok := draw[0].(map[string]any)["db"].([]any)
	if !ok || len(db) != 5 {
		t.Fatalf("draw[0].db = %v, want [x,y,w,h,pixels]", draw[0])
	}
	if db[2] != robotWidth {
		t.Errorf("draw[0].db width = %v, want %d (narrow region so AWTRIX text isn't clobbered)", db[2], robotWidth)
	}
	if got := payload["text"]; got != wantLabel {
		t.Errorf("text = %v, want %q", got, wantLabel)
	}
	if got := payload["color"]; got != wantColor {
		t.Errorf("color = %v, want %s", got, wantColor)
	}
	if got := payload["blinkText"]; got != 500 {
		t.Errorf("blinkText = %v, want 500", got)
	}
	if got := payload["textOffset"]; got != 11 {
		t.Errorf("textOffset = %v, want 11 (1-col gap after the 10-wide robot; text sits in cols 11-31 — 21 cols, fits 4-char labels)", got)
	}
	if got := payload["noScroll"]; got != true {
		t.Errorf("noScroll = %v, want true", got)
	}
	if got := payload["center"]; got != false {
		t.Errorf("center = %v, want false (AWTRIX defaults center=true and adds textOffset, clipping text past col 31)", got)
	}
	if got := payload["lifetime"]; got != 30 {
		t.Errorf("lifetime = %v, want 30", got)
	}
	if payload["prio"] != true {
		t.Errorf("prio = %v, want true (locked attention is also display-hold)", payload["prio"])
	}
	if payload["force"] != true {
		t.Errorf("force = %v, want true", payload["force"])
	}
}

func TestFrameToCustomApp_IncludesPrioForce(t *testing.T) {
	var f Frame
	paintCell(&f, 0, 0, RGB{0xff, 0x00, 0x00})
	payload := frameToCustomApp(&f, 30)
	if payload["prio"] != true {
		t.Errorf("prio = %v, want true (display hold above native rotation)", payload["prio"])
	}
	if payload["force"] != true {
		t.Errorf("force = %v, want true (push to front of app stack)", payload["force"])
	}
}

func TestRenderForCoord_LockedButNotAttentionState_SingleFrame(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "r", State: "running", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/r", cardXY, true, 30)
	frames := payload["draw"].([]any)
	if len(frames) != 1 {
		t.Fatalf("locked running: expected 1 frame, got %d", len(frames))
	}
}

func TestRenderForCoord_Counts_XOverY(t *testing.T) {
	now := time.Now()
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "s2", State: "running", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "s3", State: "running", UpdatedAt: now},
	}}
	payload := RenderForCoord(snap, "a/b/s2", cardXY, false, 30)
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// "2/3": first digit '2' starts at col 12. '2' glyph row 0 is "XXX",
	// so cols 12, 13, 14 are lit at row 1.
	for x := 12; x <= 14; x++ {
		if pixels[1*32+x] == 0 {
			t.Errorf("'2' top row should light col %d at row 1", x)
		}
	}
}

// strconvItoa avoids strconv import noise in the test file.
func strconvItoa(i int) string { return string(rune('a' + i)) }

func TestPickWinning(t *testing.T) {
	mkSession := func(state string) Session {
		return Session{Source: "a", Tool: "b", Session: "s-" + state, State: state, UpdatedAt: time.Now()}
	}
	tests := []struct {
		name      string
		sessions  []Session
		wantState string
		wantColor RGB
		wantTotal int
	}{
		{name: "empty", sessions: nil, wantState: "", wantTotal: 0},
		{name: "all idle", sessions: []Session{mkSession("idle"), mkSession("idle")}, wantState: "", wantTotal: 0},
		{name: "single running", sessions: []Session{mkSession("running")}, wantState: "running", wantColor: colorRunning, wantTotal: 1},
		{name: "waiting beats running", sessions: []Session{mkSession("running"), mkSession("waiting")}, wantState: "waiting", wantColor: colorWaiting, wantTotal: 2},
		{name: "error beats running, beaten by waiting", sessions: []Session{mkSession("running"), mkSession("error"), mkSession("waiting")}, wantState: "waiting", wantColor: colorWaiting, wantTotal: 3},
		{name: "done linger only", sessions: []Session{mkSession("done")}, wantState: "done", wantColor: colorDone, wantTotal: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			win, color, total := pickWinning(tc.sessions)
			gotState := ""
			if win != nil {
				gotState = win.State
			}
			if gotState != tc.wantState {
				t.Errorf("state = %q, want %q", gotState, tc.wantState)
			}
			if win != nil && color != tc.wantColor {
				t.Errorf("color = %v, want %v", color, tc.wantColor)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
		})
	}
}

func TestFrameToCustomApp(t *testing.T) {
	f := &Frame{}
	paintCell(f, 0, 0, RGB{0xff, 0x00, 0x00})
	paintCell(f, 31, 7, RGB{0x00, 0xff, 0x00})

	payload := frameToCustomApp(f, 30)
	draw, ok := payload["draw"].([]any)
	if !ok || len(draw) != 1 {
		t.Fatalf("payload[draw] = %v, want one-element slice", payload["draw"])
	}
	op, ok := draw[0].(map[string]any)
	if !ok {
		t.Fatalf("draw[0] = %v, want map", draw[0])
	}
	args, ok := op["db"].([]any)
	if !ok || len(args) != 5 {
		t.Fatalf(`draw[0]["db"] = %v, want 5-element slice`, op["db"])
	}
	if args[0] != 0 || args[1] != 0 || args[2] != 32 || args[3] != 8 {
		t.Fatalf("db bounds = %v, want [0 0 32 8]", args[:4])
	}
	pixels, ok := args[4].([]int)
	if !ok || len(pixels) != 256 {
		t.Fatalf("db pixels length = %d, want 256", len(pixels))
	}
	if pixels[0] != 0xff0000 {
		t.Errorf("pixel (0,0) = %#x, want 0xff0000", pixels[0])
	}
	if pixels[7*32+31] != 0x00ff00 {
		t.Errorf("pixel (31,7) = %#x, want 0x00ff00", pixels[7*32+31])
	}
	if pixels[16*5+10] != 0 {
		t.Errorf("undirty pixel = %#x, want 0", pixels[16*5+10])
	}
	if payload["lifetime"] != 30 {
		t.Errorf("lifetime = %v, want 30", payload["lifetime"])
	}
}

func TestSessionKey(t *testing.T) {
	got := sessionKey(Session{Source: "a", Tool: "b", Session: "c"})
	want := "a/b/c"
	if got != want {
		t.Errorf("sessionKey = %q, want %q", got, want)
	}
}

func TestSortedActiveKeys(t *testing.T) {
	now := time.Now()
	snap := Snapshot{Sessions: []Session{
		{Source: "src", Tool: "tool", Session: "r1", State: "running", UpdatedAt: now},
		{Source: "src", Tool: "tool", Session: "w1", State: "waiting", UpdatedAt: now},
		{Source: "src", Tool: "tool", Session: "i1", State: "idle", UpdatedAt: now},
		{Source: "src", Tool: "tool", Session: "e1", State: "error", UpdatedAt: now},
		{Source: "src", Tool: "tool", Session: "d1", State: "done", UpdatedAt: now},
	}}
	got := sortedActiveKeys(snap)
	want := []string{
		"src/tool/w1", // waiting first
		"src/tool/e1", // error
		"src/tool/r1", // running
		"src/tool/d1", // done
	}
	if !slices.Equal(got, want) {
		t.Errorf("sortedActiveKeys =\n  %v\nwant\n  %v", got, want)
	}
}

func TestPickRotated(t *testing.T) {
	keys := []string{"a", "b", "c"}
	tests := []struct {
		name string
		prev string
		keys []string
		want string
	}{
		{"empty keys", "anything", nil, ""},
		{"empty prev", "", keys, "a"},
		{"unknown prev", "z", keys, "a"},
		{"middle", "b", keys, "c"},
		{"wrap", "c", keys, "a"},
		{"single-key idempotent", "x", []string{"x"}, "x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickRotated(tc.prev, tc.keys); got != tc.want {
				t.Errorf("pickRotated(%q, %v) = %q, want %q", tc.prev, tc.keys, got, tc.want)
			}
		})
	}
}

func TestRenderIdleFrame_Shape(t *testing.T) {
	payload := RenderIdleFrame(30)
	draw, ok := payload["draw"].([]any)
	if !ok || len(draw) != 1 {
		t.Fatalf("idle payload draw[] = %v, want exactly 1 entry", payload["draw"])
	}
	db, ok := draw[0].(map[string]any)["db"].([]any)
	if !ok || len(db) != 5 {
		t.Fatalf("draw[0].db = %v, want [x,y,w,h,pixels]", draw[0])
	}
	if db[2] != robotWidth {
		t.Errorf("idle bitmap width = %v, want %d", db[2], robotWidth)
	}
	pixels, ok := db[4].([]int)
	if !ok || len(pixels) != robotWidth*8 {
		t.Fatalf("idle pixels = %v, want %d ints", db[4], robotWidth*8)
	}
	// At least one pixel must be lit (the robot is drawn) and any lit
	// pixel must be roughly 40% brightness (dim white).
	var litCount int
	for _, p := range pixels {
		if p == 0 {
			continue
		}
		litCount++
		r := (p >> 16) & 0xff
		g := (p >> 8) & 0xff
		b := p & 0xff
		if r != g || g != b {
			t.Errorf("idle pixel %#x is not white (r=g=b expected)", p)
		}
		if r < 0x40 || r > 0x80 {
			t.Errorf("idle pixel brightness %#x outside dim-white range [0x40, 0x80]", r)
		}
	}
	if litCount == 0 {
		t.Errorf("idle frame has no lit pixels — robot not drawn")
	}
	if _, hasText := payload["text"]; hasText {
		t.Errorf("idle payload should NOT have text (no WAIT/ERR while truly idle)")
	}
	if payload["prio"] != true {
		t.Errorf("prio = %v, want true (idle still holds display)", payload["prio"])
	}
	if payload["force"] != true {
		t.Errorf("force = %v, want true", payload["force"])
	}
	if payload["lifetime"] != 30 {
		t.Errorf("lifetime = %v, want 30", payload["lifetime"])
	}
	if payload["duration"] != 30 {
		t.Errorf("duration = %v, want 30", payload["duration"])
	}
}

func TestAttentionLabelAndColor(t *testing.T) {
	for _, tc := range []struct {
		state     string
		wantLabel string
		wantColor string
	}{
		{"waiting", "WAIT", "#FFC14D"},
		{"error", "ERR", "#FF3A3A"},
		{"running", "WAIT", "#FFC14D"}, // fallback path; never reached in production
	} {
		t.Run(tc.state, func(t *testing.T) {
			label, hex := attentionLabelAndColor(tc.state)
			if label != tc.wantLabel {
				t.Errorf("label = %q, want %q", label, tc.wantLabel)
			}
			if hex != tc.wantColor {
				t.Errorf("hex = %q, want %q", hex, tc.wantColor)
			}
		})
	}
}

func TestDrawSessionBar_Empty(t *testing.T) {
	f := &Frame{}
	drawSessionBar(f, nil)
	for x := 0; x < 32; x++ {
		if f.Dirty[7][x] {
			t.Errorf("col %d on row 7 lit, want all dark for empty input", x)
		}
	}
}

func TestDrawSessionBar_OneRunning(t *testing.T) {
	now := time.Now()
	sessions := []Session{
		{Source: "a", Tool: "b", Session: "s", State: "running", UpdatedAt: now},
	}
	f := &Frame{}
	drawSessionBar(f, sessions)
	if !f.Dirty[7][11] || f.Pixels[7][11] != colorRunning {
		t.Errorf("col 11 = %+v dirty=%v, want %v lit", f.Pixels[7][11], f.Dirty[7][11], colorRunning)
	}
	for x := 12; x < 32; x++ {
		if f.Dirty[7][x] {
			t.Errorf("col %d unexpectedly lit (should only be col 11 for 1 session)", x)
		}
	}
}

func TestDrawSessionBar_PriorityOrder(t *testing.T) {
	now := time.Now()
	// Arrival order: running, waiting, error.
	// Priority order: waiting > error > running.
	sessions := []Session{
		{Source: "a", Tool: "b", Session: "r", State: "running", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "w", State: "waiting", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "e", State: "error", UpdatedAt: now},
	}
	f := &Frame{}
	drawSessionBar(f, sessions)
	wants := []RGB{colorWaiting, colorError, colorRunning}
	for i, want := range wants {
		col := 11 + i
		if !f.Dirty[7][col] || f.Pixels[7][col] != want {
			t.Errorf("col %d = %+v dirty=%v, want %v", col, f.Pixels[7][col], f.Dirty[7][col], want)
		}
	}
	// No fourth pixel.
	if f.Dirty[7][14] {
		t.Errorf("col 14 lit, want dark (only 3 sessions)")
	}
}

func TestDrawSessionBar_DeterministicAcrossSliceOrder(t *testing.T) {
	now := time.Now()
	// Two same-state sessions in reversed slice orderings must produce
	// identical bars. This proves the sort is invoked (without it, slice
	// order would leak through) and that the comparator is a total order.
	// The lex *direction* (source "a" sorts before "z") is verified by
	// TestSortedActiveKeys, which exercises the same comparator shape.
	sessions1 := []Session{
		{Source: "z", Tool: "t", Session: "s", State: "waiting", UpdatedAt: now},
		{Source: "a", Tool: "t", Session: "s", State: "waiting", UpdatedAt: now},
	}
	sessions2 := []Session{sessions1[1], sessions1[0]} // reversed
	f1 := &Frame{}
	f2 := &Frame{}
	drawSessionBar(f1, sessions1)
	drawSessionBar(f2, sessions2)
	// Both bars must be identical pixel-for-pixel across row 7.
	for x := 0; x < 32; x++ {
		if f1.Dirty[7][x] != f2.Dirty[7][x] || f1.Pixels[7][x] != f2.Pixels[7][x] {
			t.Errorf("col %d differs between slice orderings: f1=%v dirty=%v vs f2=%v dirty=%v (slice order leaked through; sort must be invoked deterministically)",
				x, f1.Pixels[7][x], f1.Dirty[7][x], f2.Pixels[7][x], f2.Dirty[7][x])
		}
	}
	// And both should have exactly 2 amber pixels at cols 11 and 12.
	if f1.Pixels[7][11] != colorWaiting || f1.Pixels[7][12] != colorWaiting {
		t.Errorf("expected two amber pixels, got col11=%v col12=%v", f1.Pixels[7][11], f1.Pixels[7][12])
	}
	if f1.Dirty[7][13] {
		t.Errorf("col 13 lit, want dark (only 2 sessions)")
	}
}

func TestDrawSessionBar_IdleExcluded(t *testing.T) {
	now := time.Now()
	sessions := []Session{
		{Source: "a", Tool: "b", Session: "i", State: "idle", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "r", State: "running", UpdatedAt: now},
	}
	f := &Frame{}
	drawSessionBar(f, sessions)
	if !f.Dirty[7][11] || f.Pixels[7][11] != colorRunning {
		t.Errorf("col 11 = %v, want running green", f.Pixels[7][11])
	}
	if f.Dirty[7][12] {
		t.Errorf("col 12 lit; idle session must not produce a pixel")
	}
}

func TestDrawSessionBar_DoneIncluded(t *testing.T) {
	now := time.Now()
	sessions := []Session{
		{Source: "a", Tool: "b", Session: "d", State: "done", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "r", State: "running", UpdatedAt: now},
	}
	f := &Frame{}
	drawSessionBar(f, sessions)
	// running sorts before done by priority.
	if f.Pixels[7][11] != colorRunning {
		t.Errorf("col 11 = %v, want running green", f.Pixels[7][11])
	}
	if f.Pixels[7][12] != colorDone {
		t.Errorf("col 12 = %v, want done blue", f.Pixels[7][12])
	}
}

func TestDrawSessionBar_Overflow(t *testing.T) {
	now := time.Now()
	sessions := make([]Session, 25)
	for i := range sessions {
		sessions[i] = Session{
			Source: "a", Tool: "b", Session: fmt.Sprintf("s%02d", i),
			State: "running", UpdatedAt: now,
		}
	}
	f := &Frame{}
	drawSessionBar(f, sessions)
	// Exactly 21 pixels lit, cols 11..31.
	for x := 11; x <= 31; x++ {
		if !f.Dirty[7][x] {
			t.Errorf("col %d should be lit (overflow truncation paints first 21)", x)
		}
	}
	// No spillover above row 7.
	for y := 0; y < 7; y++ {
		for x := 0; x < 32; x++ {
			if f.Dirty[y][x] {
				t.Errorf("col %d row %d unexpectedly lit", x, y)
			}
		}
	}
}

func TestComposeFrame_CodexSprite(t *testing.T) {
	f := composeFrame(Session{Tool: "codex", State: "running"}, 1, 1, cardXY, RGB{0x2e, 0xe8, 0x5e}, nil)
	// Underscore: frame row 6 (sprite row 5, painted at y=1), cols 5–9 lit.
	for _, x := range []int{5, 6, 7, 8, 9} {
		if !f.Dirty[6][x] {
			t.Errorf("codex underscore [%d,6] lit=false, want true", x)
		}
	}
	// NOT the robot's full-width arms: row 4 (sprite row 3) cols 0 and 9 must be dark for >_.
	if f.Dirty[4][0] || f.Dirty[4][9] {
		t.Error("codex frame lit robot arm cols at row 4; want >_ geometry, not robot")
	}
}

func TestCodexSpriteCanonical(t *testing.T) {
	want := []string{
		"XX........",
		".XX.......",
		"..XX......",
		"..XX......",
		".XX.......",
		"XX...XXXXX",
	}
	if len(codexSprite) != len(want) {
		t.Fatalf("codexSprite has %d rows, want %d", len(codexSprite), len(want))
	}
	for i := range want {
		if codexSprite[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, codexSprite[i], want[i])
		}
	}
}

func TestDrawSessionBar_WaitingErrorRunningDoneMix(t *testing.T) {
	now := time.Now()
	// One of each, arrival order shuffled.
	sessions := []Session{
		{Source: "a", Tool: "b", Session: "d", State: "done", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "r", State: "running", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "e", State: "error", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "w", State: "waiting", UpdatedAt: now},
	}
	f := &Frame{}
	drawSessionBar(f, sessions)
	// Priority order: waiting (11), error (12), running (13), done (14).
	wants := []RGB{colorWaiting, colorError, colorRunning, colorDone}
	for i, want := range wants {
		col := 11 + i
		if !f.Dirty[7][col] || f.Pixels[7][col] != want {
			t.Errorf("col %d = %v dirty=%v, want %v", col, f.Pixels[7][col], f.Dirty[7][col], want)
		}
	}
}

func TestRenderForCoord_SessionBar_RowSevenReflectsSnapshot(t *testing.T) {
	now := time.Now()
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "s2", State: "waiting", UpdatedAt: now},
	}}
	payload := RenderForCoord(snap, "a/b/s1", cardXY, false, 30)
	if payload == nil {
		t.Fatal("expected non-nil payload")
	}
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// Row 7. Expect col 11 = waiting amber, col 12 = running green.
	// Derive expected values from the palette constants so the test stays
	// correct if colors are ever updated.
	wantWaiting := (int(colorWaiting.R) << 16) | (int(colorWaiting.G) << 8) | int(colorWaiting.B)
	wantRunning := (int(colorRunning.R) << 16) | (int(colorRunning.G) << 8) | int(colorRunning.B)
	got11 := pixels[7*32+11]
	got12 := pixels[7*32+12]
	if got11 != wantWaiting {
		t.Errorf("row 7 col 11 = %#06x, want %#06x (waiting amber, priority first)", got11, wantWaiting)
	}
	if got12 != wantRunning {
		t.Errorf("row 7 col 12 = %#06x, want %#06x (running green, priority second)", got12, wantRunning)
	}
	// Col 13+ on row 7 should be dark.
	for x := 13; x < 32; x++ {
		if pixels[7*32+x] != 0 {
			t.Errorf("row 7 col %d = %#06x, want 0 (only 2 sessions in snapshot)", x, pixels[7*32+x])
		}
	}
}

func TestRateText(t *testing.T) {
	cases := map[int]string{0: "0%", 7: "7%", 73: "73%", 99: "99%", 100: "99%", 150: "99%", -5: "0%"}
	for in, want := range cases {
		if got := rateText(in); got != want {
			t.Errorf("rateText(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRateColor(t *testing.T) {
	cases := map[int]RGB{0: colorRunning, 69: colorRunning, 70: colorWaiting, 89: colorWaiting, 90: colorError, 100: colorError}
	for in, want := range cases {
		if got := rateColor(in); got != want {
			t.Errorf("rateColor(%d) = %+v, want %+v", in, got, want)
		}
	}
}

func TestPercentGlyphDecodable(t *testing.T) {
	g := glyph('%')
	if g == nil {
		t.Fatal("'%' glyph missing from font3x5")
	}
	if len(g) != 5 {
		t.Fatalf("'%%' glyph has %d rows, want 5", len(g))
	}
	for i, row := range g {
		if len(row) != 3 {
			t.Errorf("'%%' glyph row %d width = %d, want 3", i, len(row))
		}
	}
}

func TestRenderForCoord_RateCard_PaintsThresholdColor(t *testing.T) {
	pct := 73
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", RateWindowPct: &pct, UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/s1", cardRate, false, 30)
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// "73%": '7' sprite row 0 is "XXX" at startY=1 → (12,1),(13,1),(14,1) lit amber.
	if got, want := pixels[1*32+12], 0xffc14d; got != want {
		t.Errorf("rate digit colour at (12,1) = %#06x, want %#06x (amber, 70-89)", got, want)
	}
}

func TestRenderForCoord_RateCard_FallsBackToXYWhenNoData(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: time.Now()},
	}}
	rate := RenderForCoord(snap, "a/b/s1", cardRate, false, 30)
	xy := RenderForCoord(snap, "a/b/s1", cardXY, false, 30)
	rp := rate["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	xp := xy["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	for i := range rp {
		if rp[i] != xp[i] {
			t.Fatalf("rate card with no data differs from xy at index %d; want identical (fallback)", i)
		}
	}
}

func TestDetailPayload_Blink(t *testing.T) {
	s := Session{Source: "a", Tool: "b", Session: "w", State: "waiting"}
	p := detailPayload(s, "WAIT", "#FFC14D", true, 30)
	if p["text"] != "WAIT" || p["color"] != "#FFC14D" {
		t.Errorf("text/color = %v/%v", p["text"], p["color"])
	}
	if p["blinkText"] != 500 {
		t.Errorf("blinkText = %v, want 500", p["blinkText"])
	}
	if p["noScroll"] != true {
		t.Errorf("noScroll = %v, want true", p["noScroll"])
	}
	if p["textOffset"] != 11 || p["center"] != false {
		t.Errorf("textOffset/center = %v/%v, want 11/false", p["textOffset"], p["center"])
	}
	db := p["draw"].([]any)[0].(map[string]any)["db"].([]any)
	if db[2] != robotWidth || len(db[4].([]int)) != robotWidth*8 {
		t.Errorf("db width/len = %v/%d, want %d/%d", db[2], len(db[4].([]int)), robotWidth, robotWidth*8)
	}
}

func TestDetailPayload_NoBlinkScrolls(t *testing.T) {
	s := Session{Source: "a", Tool: "b", Session: "r", State: "running"}
	p := detailPayload(s, "Bash: npm test", "#2EE85E", false, 30)
	if p["text"] != "Bash: npm test" {
		t.Errorf("text = %v", p["text"])
	}
	if _, has := p["blinkText"]; has {
		t.Errorf("blinkText must be absent in detail mode")
	}
	if _, has := p["noScroll"]; has {
		t.Errorf("noScroll must be absent so firmware scrolls on overflow")
	}
	if p["textOffset"] != 11 || p["center"] != false {
		t.Errorf("textOffset/center = %v/%v, want 11/false", p["textOffset"], p["center"])
	}
}

func TestCardsForSession(t *testing.T) {
	pct := 50
	if got := cardsForSession(Session{}); got != 1 {
		t.Errorf("cardsForSession(no rate) = %d, want 1", got)
	}
	if got := cardsForSession(Session{RateWindowPct: &pct}); got != 2 {
		t.Errorf("cardsForSession(with rate) = %d, want 2", got)
	}
	zero := 0
	if got := cardsForSession(Session{RateWindowPct: &zero}); got != 2 {
		t.Errorf("cardsForSession(rate=&0) = %d, want 2 (0%% is present)", got)
	}
}

func TestAvailableCards(t *testing.T) {
	pct := 50
	cases := []struct {
		name string
		s    Session
		want []int
	}{
		{"xy only", Session{State: "running"}, []int{cardXY}},
		{"xy+rate", Session{State: "running", RateWindowPct: &pct}, []int{cardXY, cardRate}},
		{"xy+tool", Session{State: "running", Activity: "Bash: x"}, []int{cardXY, cardTool}},
		{"xy+rate+tool", Session{State: "running", RateWindowPct: &pct, Activity: "Bash: x"}, []int{cardXY, cardRate, cardTool}},
		{"tool needs running", Session{State: "waiting", Activity: "Bash: x"}, []int{cardXY}},
		{"tool needs activity", Session{State: "running"}, []int{cardXY}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := availableCards(tc.s)
			if len(got) != len(tc.want) {
				t.Fatalf("availableCards = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("availableCards = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestRenderForCoord_ToolCard_EmitsScrollingDetail(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", Activity: "Bash: npm test", UpdatedAt: time.Now()},
	}}
	// availableCards = [cardXY, cardTool]; cursor 1 selects the tool card.
	payload := RenderForCoord(snap, "a/b/s1", 1, false, 30)
	if payload["text"] != "Bash: npm test" {
		t.Errorf("text = %v, want the activity string", payload["text"])
	}
	if _, has := payload["blinkText"]; has {
		t.Errorf("tool card must not blink")
	}
	if _, has := payload["noScroll"]; has {
		t.Errorf("tool card must allow scroll (no noScroll)")
	}
	if payload["color"] != "#2EE85E" {
		t.Errorf("color = %v, want running green #2EE85E", payload["color"])
	}
}

func TestRenderForCoord_CursorOutOfRange_ClampsToXY(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/s1", 2, false, 30)
	if _, hasText := payload["text"]; hasText {
		t.Errorf("clamped X/Y card must be a pixel frame, not a text payload")
	}
}

func TestRenderForCoord_LockedAttention_ScrollsActivity(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "w", State: "waiting", Activity: "Bash: rm -rf x", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/w", cardXY, true, 30)
	if payload["text"] != "Bash: rm -rf x" {
		t.Errorf("locked text = %v, want the activity string", payload["text"])
	}
	if _, has := payload["blinkText"]; has {
		t.Errorf("locked attention with activity must not blink")
	}
	if payload["color"] != "#FFC14D" {
		t.Errorf("color = %v, want waiting amber #FFC14D", payload["color"])
	}
}

func TestRenderForCoord_LockedAttention_NoActivityStillBlinks(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "w", State: "waiting", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/w", cardXY, true, 30)
	if payload["text"] != "WAIT" || payload["blinkText"] != 500 {
		t.Errorf("no-activity waiting should blink WAIT, got text=%v blink=%v", payload["text"], payload["blinkText"])
	}
}

func TestCtxText(t *testing.T) {
	g := string(glassGlyph)
	if got := ctxText(45); got != "45"+g {
		t.Errorf("ctxText(45) = %q, want %q", got, "45"+g)
	}
	if got := ctxText(150); got != "99"+g {
		t.Errorf("ctxText(150) = %q, want clamp to 99", got)
	}
	if got := ctxText(-5); got != "0"+g {
		t.Errorf("ctxText(-5) = %q, want 0", got)
	}
}

func TestGlassGlyphSprite(t *testing.T) {
	g := glyph(glassGlyph)
	if len(g) != 5 {
		t.Fatalf("glass glyph rows = %d, want 5", len(g))
	}
	for i, row := range g {
		if len(row) != 3 {
			t.Errorf("glass glyph row %d width = %d, want 3", i, len(row))
		}
	}
}

func TestAvailableCards_Ctx(t *testing.T) {
	pct := 45
	got := availableCards(Session{State: "running", ContextNumber: true, ContextPct: &pct})
	if len(got) != 2 || got[0] != cardXY || got[1] != cardCtx {
		t.Fatalf("ctx on+pct: availableCards = %v, want [cardXY cardCtx]", got)
	}
	if c := availableCards(Session{State: "running", ContextNumber: true}); len(c) != 1 {
		t.Errorf("ctx on but no pct: want [cardXY], got %v", c)
	}
	if c := availableCards(Session{State: "running", ContextPct: &pct}); len(c) != 1 {
		t.Errorf("ctx off: want [cardXY], got %v", c)
	}
	rate := 50
	all := availableCards(Session{State: "running", RateWindowPct: &rate, ContextNumber: true, ContextPct: &pct, Activity: "Bash: x"})
	want := []int{cardXY, cardRate, cardCtx, cardTool}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("order = %v, want %v", all, want)
		}
	}
}

func TestRenderForCoord_CtxCard(t *testing.T) {
	pct := 45
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", ContextNumber: true, ContextPct: &pct, UpdatedAt: time.Now()},
	}}
	// availableCards = [cardXY, cardCtx]; cursor 1 = cardCtx.
	payload := RenderForCoord(snap, "a/b/s1", 1, false, 30)
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// '4' sprite row 0 "X.X" at numStart=12,row1 → (12,1) lit green (45<70).
	if pixels[1*32+12] != 0x2ee85e {
		t.Errorf("ctx digit at (12,1) = %#06x, want green 0x2ee85e", pixels[1*32+12])
	}
	if _, hasText := payload["text"]; hasText {
		t.Error("ctx card is a pixel frame, not a text payload")
	}
}
