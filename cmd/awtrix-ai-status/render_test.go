package main

import (
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
	drawRobot(f, "running", RGB{0x2e, 0xe8, 0x5e})

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
	drawRobot(f, "error", RGB{0xff, 0x3a, 0x3a})

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

func TestDrawGlass(t *testing.T) {
	tests := []struct {
		name          string
		pct           *int
		wantOutline   bool
		wantFillRows  []int
		wantNoOutline bool
	}{
		{name: "absent — no glass at all", pct: nil, wantNoOutline: true},
		{name: "0% — outline only", pct: intPtr(0), wantOutline: true, wantFillRows: nil},
		{name: "12% — bottom interior row only", pct: intPtr(12), wantOutline: true, wantFillRows: []int{4}},
		{name: "25%", pct: intPtr(25), wantOutline: true, wantFillRows: []int{4, 3}},
		{name: "50%", pct: intPtr(50), wantOutline: true, wantFillRows: []int{4, 3, 2}},
		{name: "75%", pct: intPtr(75), wantOutline: true, wantFillRows: []int{4, 3, 2, 1}},
		{name: "100% — full", pct: intPtr(100), wantOutline: true, wantFillRows: []int{4, 3, 2, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &Frame{}
			drawGlass(f, tc.pct, RGB{0x2e, 0xe8, 0x5e})

			if tc.wantNoOutline {
				if f.Dirty[1][25] || f.Dirty[5][27] {
					t.Errorf("absent pct: outline drawn anyway")
				}
				return
			}
			if !f.Dirty[1][25] || !f.Dirty[1][30] || !f.Dirty[5][27] {
				t.Errorf("outline missing: walls or bottom not painted")
			}

			for y := 1; y <= 4; y++ {
				wantRowFilled := false
				for _, r := range tc.wantFillRows {
					if r == y {
						wantRowFilled = true
						break
					}
				}
				for x := 26; x <= 29; x++ {
					got := f.Dirty[y][x]
					if got != wantRowFilled {
						t.Errorf("[%d,%d] dirty = %v, want %v", x, y, got, wantRowFilled)
					}
				}
			}
		})
	}
}

func TestDrawRateBar(t *testing.T) {
	tests := []struct {
		name        string
		pct         *int
		wantLitCols []int
	}{
		{name: "absent — no bar", pct: nil},
		{name: "0% — no bar", pct: intPtr(0)},
		{name: "50% — half", pct: intPtr(50), wantLitCols: rangeCols(11, 21)},
		{name: "100% — full", pct: intPtr(100), wantLitCols: rangeCols(11, 31)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &Frame{}
			drawRateBar(f, tc.pct)
			litCols := []int{}
			for x := 0; x < 32; x++ {
				if f.Dirty[7][x] {
					litCols = append(litCols, x)
				}
			}
			if !equalInts(litCols, tc.wantLitCols) {
				t.Errorf("lit cols on row 7 = %v, want %v", litCols, tc.wantLitCols)
			}
		})
	}
}

func rangeCols(lo, hi int) []int {
	out := make([]int, 0, hi-lo+1)
	for x := lo; x <= hi; x++ {
		out = append(out, x)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRenderForCoord_NoActive_ReturnsNil(t *testing.T) {
	if got := RenderForCoord(Snapshot{}, "", false, 30); got != nil {
		t.Fatalf("empty snapshot: got %v, want nil", got)
	}
}

func TestRenderForCoord_PointerMissing_PicksFirst(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "c", State: "running", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "missing/key/nope", false, 30)
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
	payload := RenderForCoord(snap, "a/b/s2", false, 30)
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
			payload := RenderForCoord(snap, "a/b/w", true, 30)
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
	payload := RenderForCoord(snap, "a/b/s2", true, 30)
	assertBlinkText(t, payload, "WAIT", "#FFC14D")
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
	if got := payload["textOffset"]; got != 8 {
		t.Errorf("textOffset = %v, want 8 (text sits in cols 8-31 — 24 cols, fits 4-char labels)", got)
	}
	if got := payload["noScroll"]; got != true {
		t.Errorf("noScroll = %v, want true", got)
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
	payload := RenderForCoord(snap, "a/b/r", true, 30)
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
	payload := RenderForCoord(snap, "a/b/s2", false, 30)
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
