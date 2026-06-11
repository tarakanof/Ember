package render

import (
	"bytes"
	"encoding/json"
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
	if glyph('?') != nil {
		t.Fatalf("glyph('?') = non-nil, want nil for unsupported rune")
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
		{6, 1}, // ~6% per pixel → first pixel
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

	// Left-to-right partial row: 54% → 9px → rows 4,3 full + only leftmost col 26 in row 2.
	f = &Frame{}
	drawGlass(f, intPtr(54), fill)
	for x := 26; x <= 29; x++ {
		if !f.Dirty[4][x] || !f.Dirty[3][x] {
			t.Errorf("54%%: bottom two rows should be full (col %d)", x)
		}
	}
	if !f.Dirty[2][26] {
		t.Error("54%: leftmost col 26 of the partial row should be lit")
	}
	for _, x := range []int{27, 28, 29} {
		if f.Dirty[2][x] {
			t.Errorf("54%%: partial row fills left-to-right; col %d should still be dark", x)
		}
	}
	if litInterior(f) != 9 {
		t.Errorf("54%%: interior lit = %d, want 9", litInterior(f))
	}

	// 60% → 10px → partial row has the left PAIR (26,27); right (28,29) dark.
	f = &Frame{}
	drawGlass(f, intPtr(60), fill)
	if !f.Dirty[2][26] || !f.Dirty[2][27] || f.Dirty[2][28] || f.Dirty[2][29] {
		t.Error("60%: partial row should be left pair 26,27 only")
	}
}

func TestRenderForCoord_NoActive_ReturnsNil(t *testing.T) {
	if got := RenderForCoord(Snapshot{}, "", cardSource, false, 30, nil); got != nil {
		t.Fatalf("empty snapshot: got %v, want nil", got)
	}
}

func TestRenderForCoord_PointerMissing_PicksFirst(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "c", State: "running", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "missing/key/nope", cardSource, false, 30, nil)
	if payload == nil {
		t.Fatal("expected non-nil payload for single running session")
	}
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// Source "a" → sourceCardText "A"; 'A' glyph row 0 "XXX" at numStart=9, drawY=1.
	// cols 9,10,11 should be lit (white, no SourceColor set).
	if pixels[1*32+9] == 0 {
		t.Errorf("expected source 'A' first column lit at (9,1)")
	}
}

func TestRenderForCoord_TwoActive_HonorsPointer(t *testing.T) {
	purple := "#aa66ff"
	green := "#2ee85e"
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", SourceColor: &purple, UpdatedAt: time.Now()},
		{Source: "a", Tool: "b", Session: "s2", State: "running", SourceColor: &green, UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/s2", cardSource, false, 30, nil)
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// numStart=9: first digit '1' middle col → matrix (10, 1). Should be green.
	if got, want := pixels[1*32+10], 0x2ee85e; got != want {
		t.Errorf("digit colour at (10,1) = %#06x, want %#06x (s2 SourceColor)", got, want)
	}
}

func TestRenderForCoord_LockedAttention_EmitsBlinkText(t *testing.T) {
	tests := []struct {
		state     string
		wantLabel string
		wantColor string
	}{
		// Source "a" is appended (uppercased) to the attention label.
		{"waiting", "WAIT A", "#FFC14D"},
		{"error", "ERR A", "#FF3A3A"},
	}
	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			snap := Snapshot{Sessions: []Session{
				{Source: "a", Tool: "b", Session: "w", State: tc.state, UpdatedAt: time.Now()},
			}}
			payload := RenderForCoord(snap, "a/b/w", cardSource, true, 30, nil)
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
	// Source "a" is appended to the attention label.
	payload := RenderForCoord(snap, "a/b/s2", cardSource, true, 30, nil)
	assertBlinkText(t, payload, "WAIT A", "#FFC14D")
}

// TestRenderForCoord_LockedAttention_PixelGeometry asserts the locked payload
// uses the 8×8 tool icon (usageIconClaude robot-face for a non-codex tool),
// leaving cols 8-31 clear for the native blink text. Guards against regression
// to the old 10-wide robot sprite.
func TestRenderForCoord_LockedAttention_PixelGeometry(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "w", State: "waiting", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/w", cardSource, true, 30, nil)
	db := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)
	if db[2] != 8 || db[3] != 8 {
		t.Fatalf("locked icon = %vx%v, want 8x8 tool icon", db[2], db[3])
	}
	pixels, ok := db[4].([]int)
	if !ok || len(pixels) != 64 {
		t.Fatalf("locked pixel array = %v, want []int of length 64 (8×8)", db[4])
	}
	at := func(x, y int) int { return pixels[y*8+x] }
	// usageIconClaude: row0 "..X..X.." lights cols 2 & 5 (ears).
	if at(2, 0) == 0 || at(5, 0) == 0 {
		t.Errorf("row0 ears (cols 2,5) dark, want lit — not the Claude robot-face icon")
	}
	// row4 "XXXXXXXX" — fully lit.
	for x := 0; x < 8; x++ {
		if at(x, 4) == 0 {
			t.Errorf("row4 col %d dark, want lit (full row)", x)
		}
	}
	// row7 "........" — fully dark, and nothing painted in cols 8-31 (text region).
	for x := 0; x < 8; x++ {
		if at(x, 7) != 0 {
			t.Errorf("row7 col %d lit, want dark", x)
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
	if db[2] != 8 {
		t.Errorf("draw[0].db width = %v, want 8 (8×8 tool icon so AWTRIX text isn't clobbered)", db[2])
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
	if got := payload["textOffset"]; got != 9 {
		t.Errorf("textOffset = %v, want 9 (1-col gap after the 8×8 icon; text sits in cols 9-31 — 23 cols)", got)
	}
	// noScroll is set only when the label fits the 22 free cols (≤5 chars);
	// longer labels (source appended) must be allowed to scroll.
	if len(wantLabel) <= 5 {
		if got := payload["noScroll"]; got != true {
			t.Errorf("noScroll = %v, want true (short label fits without scroll)", got)
		}
	} else {
		if _, has := payload["noScroll"]; has {
			t.Errorf("noScroll must be absent for long label %q (firmware must scroll)", wantLabel)
		}
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

func TestFrameToCustomApp_Hold_IncludesPrioForce(t *testing.T) {
	var f Frame
	paintCell(&f, 0, 0, RGB{0xff, 0x00, 0x00})
	payload := frameToCustomApp(&f, 30, true)
	if payload["prio"] != true {
		t.Errorf("prio = %v, want true (display hold above native rotation)", payload["prio"])
	}
	if payload["force"] != true {
		t.Errorf("force = %v, want true (push to front of app stack)", payload["force"])
	}
	if payload["duration"] != 30 {
		t.Errorf("duration = %v, want lifetime (held frames own the screen)", payload["duration"])
	}
}

func TestFrameToCustomApp_NoHold_RotatesNatively(t *testing.T) {
	var f Frame
	paintCell(&f, 0, 0, RGB{0xff, 0x00, 0x00})
	payload := frameToCustomApp(&f, 30, false)
	if _, has := payload["prio"]; has {
		t.Errorf("prio must be absent without hold (frame rotates like any app)")
	}
	if _, has := payload["force"]; has {
		t.Errorf("force must be absent without hold (no snap-to-front on update)")
	}
	if payload["duration"] != rotateDwellSeconds {
		t.Errorf("duration = %v, want %d (short dwell, same as the weather tiles)", payload["duration"], rotateDwellSeconds)
	}
	if payload["lifetime"] != 30 {
		t.Errorf("lifetime = %v, want 30 (crash-safe eviction unchanged)", payload["lifetime"])
	}
}

func TestRenderForCoord_Running_NoDisplayHold(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/s1", cardSource, false, 30, nil)
	if _, has := payload["prio"]; has {
		t.Errorf("running frame must not set prio (hold is reserved for locked attention)")
	}
	if _, has := payload["force"]; has {
		t.Errorf("running frame must not set force (updates must not snap the display back)")
	}
	if payload["duration"] != rotateDwellSeconds {
		t.Errorf("duration = %v, want %d", payload["duration"], rotateDwellSeconds)
	}
	if payload["lifetime"] != 30 {
		t.Errorf("lifetime = %v, want 30", payload["lifetime"])
	}
}

func TestRenderForCoord_RunningToolCard_NoDisplayHold(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", Activity: "Bash: npm test", UpdatedAt: time.Now()},
	}}
	// AvailableCards = [cardSource, cardTool]; cursor 1 selects the tool card.
	payload := RenderForCoord(snap, "a/b/s1", 1, false, 30, nil)
	if _, has := payload["prio"]; has {
		t.Errorf("tool-card detail must not set prio while running")
	}
	if _, has := payload["force"]; has {
		t.Errorf("tool-card detail must not set force while running")
	}
	if payload["duration"] != rotateDwellSeconds {
		t.Errorf("duration = %v, want %d", payload["duration"], rotateDwellSeconds)
	}
}

func TestRenderForCoord_LockedButNotAttentionState_SingleFrame(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "r", State: "running", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/r", cardSource, true, 30, nil)
	frames := payload["draw"].([]any)
	if len(frames) != 1 {
		t.Fatalf("locked running: expected 1 frame, got %d", len(frames))
	}
}

func TestRenderForCoord_SourceCard_DrawsSourceGlyph(t *testing.T) {
	now := time.Now()
	snap := Snapshot{Sessions: []Session{
		{Source: "mbp", Tool: "b", Session: "s1", State: "running", UpdatedAt: now},
	}}
	payload := RenderForCoord(snap, "mbp/b/s1", cardSource, false, 30, nil)
	pixels := payload["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// source card shows "MBP": 'M' glyph row 0 is "XXX" at numStart=9 → cols 9,10,11 lit in white.
	want := (0xff << 16) | (0xff << 8) | 0xff // colorWhite when no SourceColor
	for x := 9; x <= 11; x++ {
		if pixels[1*32+x] != want {
			t.Errorf("source-card 'M' top row col %d = %#06x, want white %#06x", x, pixels[1*32+x], want)
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
			win, color, total := PickWinning(tc.sessions)
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

	payload := frameToCustomApp(f, 30, false)
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
	got := SortedActiveKeys(snap)
	want := []string{
		"src/tool/w1", // waiting first
		"src/tool/e1", // error
		"src/tool/r1", // running
		"src/tool/d1", // done
	}
	if !slices.Equal(got, want) {
		t.Errorf("SortedActiveKeys =\n  %v\nwant\n  %v", got, want)
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
			if got := PickRotated(tc.prev, tc.keys); got != tc.want {
				t.Errorf("PickRotated(%q, %v) = %q, want %q", tc.prev, tc.keys, got, tc.want)
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
	if db[2] != 8 {
		t.Errorf("idle bitmap width = %v, want 8 (8×8 icon)", db[2])
	}
	pixels, ok := db[4].([]int)
	if !ok || len(pixels) != 64 {
		t.Fatalf("idle pixels = %v, want 64 ints", db[4])
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
	// No SourceColor → neutral body; state colour covers the "_" cursor overlay.
	f := ComposeFrame(Session{Tool: "codex", State: "running"}, cardSource, nil, nil, time.Now())
	// Row 0 col 0 lights the chevron body in iconNeutral.
	if !f.Dirty[0][0] || f.Pixels[0][0] != iconNeutral {
		t.Errorf("codex icon (0,0) not lit in neutral body colour: %v", f.Pixels[0][0])
	}
	// Row 6 cols 3-6 are the "_" cursor — state colour (running = green).
	for _, x := range []int{3, 4, 5, 6} {
		if !f.Dirty[6][x] || f.Pixels[6][x] != colorRunning {
			t.Errorf("codex cursor col %d not lit in running colour: dirty=%v val=%v", x, f.Dirty[6][x], f.Pixels[6][x])
		}
	}
	// Row 6 col 0 is chevron body (iconNeutral), not cursor.
	if !f.Dirty[6][0] || f.Pixels[6][0] != iconNeutral {
		t.Errorf("codex chevron tail (6,0) should be neutral body: %v", f.Pixels[6][0])
	}
	// Distinct from the Claude robot-face, which lights (2,0) not (0,0).
	if f.Dirty[0][2] {
		t.Error("codex frame lit (2,0) — that's the Claude icon, not codex")
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

func TestDrawRateBar(t *testing.T) {
	litCols := func(f *Frame) []int {
		var out []int
		for x := 0; x < 32; x++ {
			if f.Dirty[7][x] {
				out = append(out, x)
			}
		}
		return out
	}
	_ = litCols // dimmed bar paints the whole content area (track or fill); count fills below
	// New design: usage-widget dimmed threshold bar over content cols 8-31.
	// Every content cell is painted (track or fill), so we count dimThreshold fills.
	fillCount := func(f *Frame, pct int) int {
		n := 0
		for x := 8; x < 32; x++ {
			if f.Pixels[7][x] == dimThreshold(pct) {
				n++
			}
		}
		return n
	}
	tests := []struct {
		name      string
		pct, want int
	}{
		{name: "zero — no fill", pct: 0, want: 0},
		{name: "negative — clamped 0", pct: -5, want: 0},
		{name: "tiny non-zero — min 1px", pct: 2, want: 1}, // round(0.48)=0 → forced to 1
		{name: "half — 12px", pct: 50, want: 12},
		{name: "full — 24px", pct: 100, want: 24},
		{name: "over 100 — clamped full", pct: 130, want: 24},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &Frame{}
			drawRateBar(f, tc.pct, colorRunning)
			if got := fillCount(f, tc.pct); got != tc.want {
				t.Errorf("pct=%d fill = %d, want %d", tc.pct, got, tc.want)
			}
		})
	}
	// Colour is the dimmed threshold, derived from pct (the arg is ignored).
	f := &Frame{}
	drawRateBar(f, 50, colorError)
	if f.Pixels[7][8] != dimThreshold(50) {
		t.Errorf("fill colour = %v, want dimThreshold(50) %v", f.Pixels[7][8], dimThreshold(50))
	}
}

func rangeInts(lo, hi int) []int {
	out := make([]int, 0, hi-lo+1)
	for x := lo; x <= hi; x++ {
		out = append(out, x)
	}
	return out
}

func TestComposeFrame_RateBottomBar(t *testing.T) {
	now := time.Now()
	// All sessions passed to ComposeFrame; the displayed session `s` governs row 7.
	others := []Session{
		{Source: "a", Tool: "claude", Session: "s1", State: "running", UpdatedAt: now},
		{Source: "a", Tool: "claude", Session: "s2", State: "waiting", UpdatedAt: now},
	}

	// Toggle ON + rate present → dimmed threshold bar over content cols 8-31;
	// 50% → 12 fills (cols 8-19) in dimThreshold(50), cols 20+ are track.
	rw := 50
	s := Session{Source: "a", Tool: "claude", Session: "s1", State: "running",
		RateBottomBar: true, RateWindowPct: &rw, UpdatedAt: now}
	f := ComposeFrame(s, cardSource, nil, others, time.Now())
	for x := 8; x <= 19; x++ {
		if f.Pixels[7][x] != dimThreshold(50) {
			t.Fatalf("rate bar: col %d = %v, want fill %v", x, f.Pixels[7][x], dimThreshold(50))
		}
	}
	if f.Pixels[7][20] == dimThreshold(50) {
		t.Errorf("rate bar over-filled past col 19")
	}

	// Toggle OFF → session-count bar (2 sessions → cols 11,12 by priority: waiting, running).
	sOff := Session{Source: "a", Tool: "claude", Session: "s1", State: "running", UpdatedAt: now}
	fOff := ComposeFrame(sOff, cardSource, nil, others, time.Now())
	if fOff.Pixels[7][11] != colorWaiting || fOff.Pixels[7][12] != colorRunning {
		t.Errorf("session bar: got col11=%v col12=%v, want waiting,running", fOff.Pixels[7][11], fOff.Pixels[7][12])
	}

	// Toggle ON but no rate data → graceful fallback to the session-count bar.
	sFallback := Session{Source: "a", Tool: "claude", Session: "s1", State: "running",
		RateBottomBar: true, UpdatedAt: now}
	fFallback := ComposeFrame(sFallback, cardSource, nil, others, time.Now())
	if fFallback.Pixels[7][11] != colorWaiting || fFallback.Pixels[7][12] != colorRunning {
		t.Errorf("fallback: got col11=%v col12=%v, want session bar (waiting,running)", fFallback.Pixels[7][11], fFallback.Pixels[7][12])
	}
}

func TestRenderForCoord_SessionBar_RowSevenReflectsSnapshot(t *testing.T) {
	now := time.Now()
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: now},
		{Source: "a", Tool: "b", Session: "s2", State: "waiting", UpdatedAt: now},
	}}
	payload := RenderForCoord(snap, "a/b/s1", cardSource, false, 30, nil)
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

func TestDetailPayload_Blink(t *testing.T) {
	s := Session{Source: "a", Tool: "b", Session: "w", State: "waiting"}
	p := detailPayload(s, "WAIT", "#FFC14D", true, 30, true)
	if p["text"] != "WAIT" || p["color"] != "#FFC14D" {
		t.Errorf("text/color = %v/%v", p["text"], p["color"])
	}
	if p["blinkText"] != 500 {
		t.Errorf("blinkText = %v, want 500", p["blinkText"])
	}
	if p["noScroll"] != true {
		t.Errorf("noScroll = %v, want true", p["noScroll"])
	}
	if p["textOffset"] != 9 || p["center"] != false {
		t.Errorf("textOffset/center = %v/%v, want 9/false", p["textOffset"], p["center"])
	}
	db := p["draw"].([]any)[0].(map[string]any)["db"].([]any)
	if db[2] != 8 || len(db[4].([]int)) != 64 {
		t.Errorf("db width/len = %v/%d, want 8/64", db[2], len(db[4].([]int)))
	}
}

func TestDetailPayload_NoBlinkScrolls(t *testing.T) {
	s := Session{Source: "a", Tool: "b", Session: "r", State: "running"}
	p := detailPayload(s, "Bash: npm test", "#2EE85E", false, 30, false)
	if p["text"] != "Bash: npm test" {
		t.Errorf("text = %v", p["text"])
	}
	if _, has := p["blinkText"]; has {
		t.Errorf("blinkText must be absent in detail mode")
	}
	if _, has := p["noScroll"]; has {
		t.Errorf("noScroll must be absent so firmware scrolls on overflow")
	}
	if p["textOffset"] != 9 || p["center"] != false {
		t.Errorf("textOffset/center = %v/%v, want 9/false", p["textOffset"], p["center"])
	}
}

func TestCardsForSession(t *testing.T) {
	// CardsForSession is a thin wrapper over AvailableCards; nil view → source only.
	if got := CardsForSession(Session{Source: "mbp"}, nil); got != 1 {
		t.Errorf("CardsForSession(nil view) = %d, want 1", got)
	}
	p7 := 30
	u := &UsageView{FiveHourPct: 80, SevenDayPct: &p7}
	if got := CardsForSession(Session{Source: "mbp", RateBottomBar: true}, u); got != 3 {
		// source + usage5h + usage7d (no reset card in rate-bar mode)
		t.Errorf("CardsForSession(rate-bar, 5h+7d) = %d, want 3", got)
	}
}

func TestAvailableCards(t *testing.T) {
	// nil view: only source and tool cards are possible.
	cases := []struct {
		name string
		s    Session
		want []int
	}{
		{"source only", Session{Source: "mbp", State: "running"}, []int{cardSource}},
		{"source+tool", Session{Source: "mbp", State: "running", Activity: "Bash: x"}, []int{cardSource, cardTool}},
		{"tool needs running", Session{Source: "mbp", State: "waiting", Activity: "Bash: x"}, []int{cardSource}},
		{"tool needs activity", Session{Source: "mbp", State: "running"}, []int{cardSource}},
		{"no source → empty", Session{State: "running"}, []int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AvailableCards(tc.s, nil)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("AvailableCards = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenderForCoord_ToolCard_EmitsScrollingDetail(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", Activity: "Bash: npm test", UpdatedAt: time.Now()},
	}}
	// AvailableCards = [cardSource, cardTool]; cursor 1 selects the tool card.
	payload := RenderForCoord(snap, "a/b/s1", 1, false, 30, nil)
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

func TestRenderForCoord_CursorOutOfRange_ClampsToFirstCard(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/s1", 2, false, 30, nil)
	if _, hasText := payload["text"]; hasText {
		t.Errorf("out-of-range card index must clamp to first card and return a pixel frame, not a text payload")
	}
}

func TestRenderForCoord_LockedAttention_ActivityDoesNotSubstituteLabel(t *testing.T) {
	// 2026-06-11 redesign: activity detail no longer substitutes the attention
	// label. The frame always shows "WAIT <SOURCE>" so the user knows which
	// agent/computer needs them, regardless of what tool call is in progress.
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "w", State: "waiting", Activity: "Bash: rm -rf x", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/w", cardSource, true, 30, nil)
	if payload["text"] != "WAIT A" {
		t.Errorf("locked text = %v, want WAIT A (source label, not activity)", payload["text"])
	}
	if payload["blinkText"] != 500 {
		t.Errorf("locked attention must blink; blinkText = %v", payload["blinkText"])
	}
	if payload["color"] != "#FFC14D" {
		t.Errorf("color = %v, want waiting amber #FFC14D", payload["color"])
	}
}

func TestRenderForCoord_LockedAttention_NoActivityStillBlinks(t *testing.T) {
	// Source "a" is appended — activity (absent here) never drove the label
	// anyway after the 2026-06-11 redesign.
	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "w", State: "waiting", UpdatedAt: time.Now()},
	}}
	payload := RenderForCoord(snap, "a/b/w", cardSource, true, 30, nil)
	if payload["text"] != "WAIT A" || payload["blinkText"] != 500 {
		t.Errorf("waiting should blink WAIT A, got text=%v blink=%v", payload["text"], payload["blinkText"])
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

func TestResetText(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	tests := []struct {
		name      string
		resetAt   int64
		wantText  string
		wantColor RGB
	}{
		{"4h10m left → 5h green", 1_000_000 + 4*3600 + 600, "5" + string(resetGlyph), colorRunning},
		{"exactly 2h → 2h green", 1_000_000 + 2*3600, "2" + string(resetGlyph), colorRunning},
		{"40m left → 1h amber", 1_000_000 + 40*60, "1" + string(resetGlyph), colorWaiting},
		{"already past → 0 amber", 1_000_000 - 10, "0" + string(resetGlyph), colorWaiting},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, color := resetText(tc.resetAt, base)
			if text != tc.wantText || color != tc.wantColor {
				t.Errorf("resetText = (%q,%v), want (%q,%v)", text, color, tc.wantText, tc.wantColor)
			}
		})
	}
}

func TestResetGlyphInFont(t *testing.T) {
	g := glyph(resetGlyph)
	if g == nil || len(g) != 5 {
		t.Fatalf("resetGlyph not a 5-row sprite: %v", g)
	}
}

func TestDrawToolIcon8(t *testing.T) {
	red := RGB{0xff, 0, 0}
	blue := RGB{0x00, 0x00, 0xff}
	var fc Frame
	drawToolIcon8(&fc, Session{Tool: "claude", State: "error"}, red, blue)
	// Body pixel (row 0, col 2 — lit in usageIconClaude, not in claudeEyes8) is red.
	if !fc.Dirty[0][2] || fc.Pixels[0][2] != red {
		t.Errorf("claude icon body pixel (2,0) not painted in body colour: %v", fc.Pixels[0][2])
	}
	// Eye pixel (row 2, col 2 — a hole in body, lit in claudeEyes8) is blue.
	if !fc.Dirty[2][2] || fc.Pixels[2][2] != blue {
		t.Errorf("claude eye pixel (2,2) not painted in feature colour: %v", fc.Pixels[2][2])
	}
	for y := 0; y < 8; y++ {
		for x := 8; x < 32; x++ {
			if fc.Dirty[y][x] {
				t.Fatalf("icon painted outside 0-7 at %d,%d", x, y)
			}
		}
	}
	var fx Frame
	drawToolIcon8(&fx, Session{Tool: "codex"}, red, blue)
	if !fx.Dirty[0][0] {
		t.Errorf("codex icon top-left not painted")
	}
	if fx.Dirty[0][2] {
		t.Errorf("codex must differ from claude at (2,0)")
	}
	px := composeToolIconPixels(Session{Tool: "claude"}, red, blue)
	if len(px) != 64 {
		t.Fatalf("composeToolIconPixels len = %d, want 64", len(px))
	}
}

func TestComposeFrameUsesToolIcon(t *testing.T) {
	s := Session{Source: "a", Tool: "claude", Session: "s", State: "running"}
	f := ComposeFrame(s, cardSource, nil, []Session{s}, time.Now())
	if !f.Dirty[0][2] {
		t.Errorf("icon not drawn at (2,0)")
	}
	lit := false
	for y := 1; y <= 5; y++ {
		if f.Dirty[y][9] || f.Dirty[y][10] || f.Dirty[y][11] {
			lit = true
		}
	}
	if !lit {
		t.Errorf("source-card digits not drawn at col 9")
	}
}

func TestRateBarDimmedThreshold(t *testing.T) {
	var f Frame
	drawRateBar(&f, 50, rateColor(50)) // colour arg ignored; uses dimThreshold
	filled := 0
	for x := 8; x < 32; x++ {
		if f.Pixels[7][x] == dimThreshold(50) {
			filled++
		}
	}
	if filled != 12 { // 24-wide content, 50% -> 12
		t.Errorf("filled = %d, want 12 dimmed cells", filled)
	}
	if f.Pixels[7][8] != dimThreshold(50) {
		t.Errorf("bar must start at col 8 (content area)")
	}
}

func TestAvailableCardsUsageGating(t *testing.T) {
	s := Session{Source: "mbp", Tool: "claude", State: "running", RateBottomBar: true}

	// No usage view -> only the source card (rate/ctx/reset cards are gone).
	cards := AvailableCards(s, nil)
	if len(cards) != 1 || cards[0] != cardSource {
		t.Fatalf("nil view: got %v, want [cardSource]", cards)
	}

	// Full view, rate-bar mode: 5h face (clock), 7d, two models. No cardUsageReset
	// (the 5h face already shows the clock; pct lives on the bar).
	p7 := 42
	u := &UsageView{FiveHourPct: 87, ResetLabel: "17:30", SevenDayPct: &p7,
		Models: []ModelUsage{{Marker: "OP", Pct: 51}, {Marker: "SO", Pct: 12}}}
	cards = AvailableCards(s, u)
	want := []int{cardSource, cardUsage5h, cardUsage7d, cardUsageModelA, cardUsageModelB}
	if !slices.Equal(cards, want) {
		t.Fatalf("rate-bar mode: got %v, want %v", cards, want)
	}

	// Sessions-bar mode: pct moves into the 5h face, so the clock needs its own card.
	s.RateBottomBar = false
	cards = AvailableCards(s, u)
	want = []int{cardSource, cardUsage5h, cardUsageReset, cardUsage7d, cardUsageModelA, cardUsageModelB}
	if !slices.Equal(cards, want) {
		t.Fatalf("sessions-bar mode: got %v, want %v", cards, want)
	}

	// View without optional data: just the 5h face (+ reset since not rate-bar mode, ResetAt set).
	cards = AvailableCards(s, &UsageView{FiveHourPct: 61, ResetAt: 1})
	want = []int{cardSource, cardUsage5h, cardUsageReset}
	if !slices.Equal(cards, want) {
		t.Fatalf("minimal view: got %v, want %v", cards, want)
	}
}

func TestDetailPayloadUses8pxIcon(t *testing.T) {
	s := Session{Source: "a", Tool: "claude", Session: "s", State: "waiting"}
	p := detailPayload(s, "WAIT", "#FFC14D", true, 30, true)
	draws := p["draw"].([]any)
	db := draws[0].(map[string]any)["db"].([]any)
	if db[2] != 8 || db[3] != 8 {
		t.Errorf("locked db not 8×8: %v %v", db[2], db[3])
	}
	if p["textOffset"] != 9 {
		t.Errorf("textOffset = %v, want 9", p["textOffset"])
	}
}

func TestIdleFrameUses8pxIcon(t *testing.T) {
	p := RenderIdleFrame(30)
	draws := p["draw"].([]any)
	db := draws[0].(map[string]any)["db"].([]any)
	if db[2] != 8 || db[3] != 8 {
		t.Errorf("idle db not 8×8: %v %v", db[2], db[3])
	}
	if _, hasText := p["text"]; hasText {
		t.Error("idle frame must stay text-free")
	}
}

func TestComposeFrameUsageFaces(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	p7 := 95
	ctx := 47
	u := &UsageView{FiveHourPct: 87, ResetLabel: "17:30", SevenDayPct: &p7,
		Models: []ModelUsage{{Marker: "OP", Pct: 51}, {Marker: "SO", Pct: 12}}}
	s := Session{Source: "mbp", Tool: "claude", State: "running", RateBottomBar: true, ContextPct: &ctx}

	// requireUnit asserts the gray window label at the unit slot and that the
	// context glass is absent: units paint usageGray, so any glassWall-coloured
	// pixel in the glass columns betrays a drawn glass.
	requireUnit := func(t *testing.T, f *Frame, face string) {
		t.Helper()
		if got := f.Pixels[1][unitStart]; got != usageGray {
			t.Fatalf("%s: unit pixel = %v, want gray %v", face, got, usageGray)
		}
		for y := glassTopRow; y <= glassBottomRow; y++ {
			for x := glassLeft; x <= glassRight; x++ {
				if f.Dirty[y][x] && f.Pixels[y][x] == glassWall {
					t.Fatalf("%s: context glass must not be drawn on usage faces (wall at %d,%d)", face, x, y)
				}
			}
		}
	}

	// 5h face in rate-bar mode: clock at numStart — '1' row 0 is ".X." so its
	// lit pixel is x=numStart+1 — plus the "5h" unit where the glass was.
	f := ComposeFrame(s, cardUsage5h, u, []Session{s}, now)
	if !f.Dirty[1][10] {
		t.Fatal("5h face: clock not painted")
	}
	if f.Pixels[1][10] != colorWhite {
		t.Fatalf("5h face: clock pixel color = %v, want white %v", f.Pixels[1][10], colorWhite)
	}
	requireUnit(t, &f, "5h clock face")

	// 5h face in sessions-bar mode: "87%" digits in rateColor(87)=amber + unit.
	s2 := s
	s2.RateBottomBar = false
	f = ComposeFrame(s2, cardUsage5h, u, []Session{s2}, now)
	if got := f.Pixels[1][9]; got != rateColor(87) {
		t.Fatalf("5h pct face: pixel = %v, want amber %v", got, rateColor(87))
	}
	requireUnit(t, &f, "5h pct face")

	// 7d face: red "95%" at numStart, gray "7d" unit at the right edge.
	f = ComposeFrame(s, cardUsage7d, u, []Session{s}, now)
	if got := f.Pixels[1][9]; got != rateColor(95) {
		t.Fatalf("7d pct: pixel = %v, want red %v", got, rateColor(95))
	}
	requireUnit(t, &f, "7d face")

	// Model face: green "51%" + gray "OP" unit.
	f = ComposeFrame(s, cardUsageModelA, u, []Session{s}, now)
	if got := f.Pixels[1][9]; got != rateColor(51) {
		t.Fatalf("model pct: pixel = %v, want green %v", got, rateColor(51))
	}
	requireUnit(t, &f, "model A face")

	// Model B face: green "12%" + gray "SO" unit. '1' row 0 is ".X." so the
	// first lit pct pixel is x=10, not 9.
	f = ComposeFrame(s, cardUsageModelB, u, []Session{s}, now)
	if got := f.Pixels[1][10]; got != rateColor(12) {
		t.Fatalf("model B pct: pixel = %v, want green %v", got, rateColor(12))
	}
	requireUnit(t, &f, "model B face")

	// Reset face without a label: hourglass fallback (resetText colour) + "5h"
	// unit (the countdown belongs to the 5h window).
	u2 := &UsageView{FiveHourPct: 61, ResetAt: now.Add(3 * time.Hour).Unix()}
	f = ComposeFrame(s2, cardUsageReset, u2, []Session{s2}, now)
	if got := f.Pixels[1][9]; got != colorRunning { // 3 hours left -> green
		t.Fatalf("reset fallback: pixel = %v, want green %v", got, colorRunning)
	}
	requireUnit(t, &f, "reset face")

	// The source card keeps the context glass: (30,1) is its right wall.
	f = ComposeFrame(s, cardSource, u, []Session{s}, now)
	if got := f.Pixels[1][30]; got != glassWall {
		t.Fatalf("source card glass wall: pixel = %v, want %v", got, glassWall)
	}
}

func TestRenderIdleUsagePayload(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	if RenderIdleUsagePayload(nil, 0, now, 30) != nil {
		t.Fatal("no views: want nil payload")
	}
	if RenderIdleUsagePayload(map[string]*UsageView{}, 0, now, 30) != nil {
		t.Fatal("empty views: want nil payload")
	}

	p7 := 42
	views := map[string]*UsageView{
		"claude": {FiveHourPct: 87, ResetLabel: "17:30", SevenDayPct: &p7},
	}
	p0 := RenderIdleUsagePayload(views, 0, now, 30) // 5h face
	p1 := RenderIdleUsagePayload(views, 1, now, 30) // 7d face
	p2 := RenderIdleUsagePayload(views, 2, now, 30) // wraps to 5h
	if p0 == nil || p1 == nil {
		t.Fatal("hot view: want non-nil payloads")
	}
	b0, _ := json.Marshal(p0)
	b1, _ := json.Marshal(p1)
	b2, _ := json.Marshal(p2)
	if bytes.Equal(b0, b1) {
		t.Fatal("faces 0 and 1 should differ")
	}
	if !bytes.Equal(b0, b2) {
		t.Fatal("cursor should wrap (face 2 == face 0)")
	}

	// The idle 5h face carries the gray "5h" unit label: '5' top-left lights
	// (unitStart, 1), i.e. pixel index 1*32+unitStart in the db payload.
	px := p0["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	wantGray := (int(usageGray.R) << 16) | (int(usageGray.G) << 8) | int(usageGray.B)
	if got := px[1*32+unitStart]; got != wantGray {
		t.Errorf("idle 5h face unit pixel = %#06x, want gray %#06x", got, wantGray)
	}
}

func TestRenderIdleUsagePayload_MultiTool(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	// claude: 5h + 7d faces (2 faces).
	// codex: 5h only — no SevenDayPct, no ResetLabel, but ResetAt set.
	// Fixed order: claude faces first, then codex → 3 faces total.
	p7 := 42
	views := map[string]*UsageView{
		"claude": {FiveHourPct: 87, ResetLabel: "17:30", SevenDayPct: &p7},
		"codex":  {FiveHourPct: 73, ResetAt: now.Add(2 * time.Hour).Unix()},
	}

	p0 := RenderIdleUsagePayload(views, 0, now, 30) // claude 5h
	p1 := RenderIdleUsagePayload(views, 1, now, 30) // claude 7d
	p2 := RenderIdleUsagePayload(views, 2, now, 30) // codex 5h
	p3 := RenderIdleUsagePayload(views, 3, now, 30) // wraps → claude 5h

	if p0 == nil || p1 == nil || p2 == nil || p3 == nil {
		t.Fatal("all cursors over hot tools: want non-nil payloads")
	}

	b0, _ := json.Marshal(p0)
	b1, _ := json.Marshal(p1)
	b2, _ := json.Marshal(p2)
	b3, _ := json.Marshal(p3)

	if bytes.Equal(b0, b1) {
		t.Error("cursor 0 (claude 5h) and cursor 1 (claude 7d) must differ")
	}
	if bytes.Equal(b0, b2) {
		t.Error("cursor 0 (claude 5h) and cursor 2 (codex 5h) must differ")
	}
	if bytes.Equal(b1, b2) {
		t.Error("cursor 1 (claude 7d) and cursor 2 (codex 5h) must differ")
	}
	if !bytes.Equal(b0, b3) {
		t.Error("cursor 3 must wrap to cursor 0 (claude 5h)")
	}
}

func TestRenderForCoordUsesUsageView(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	s := Session{Source: "mbp", Tool: "claude", Session: "x", State: "running",
		RateBottomBar: true, UpdatedAt: now}
	snap := Snapshot{Now: now, Sessions: []Session{s}}
	views := map[string]*UsageView{"claude": {FiveHourPct: 87, ResetLabel: "17:30"}}

	// card index 1 = cardUsage5h (after cardSource) only when views are passed.
	withUsage := RenderForCoord(snap, s.Key(), 1, false, 30, views)
	without := RenderForCoord(snap, s.Key(), 1, false, 30, nil)
	bu, _ := json.Marshal(withUsage)
	bw, _ := json.Marshal(without)
	if bytes.Equal(bu, bw) {
		t.Fatal("usage view should change the rendered card")
	}
}
