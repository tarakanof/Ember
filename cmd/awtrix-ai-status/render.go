package main

import (
	"slices"
	"strconv"
)

// RGB is a 24-bit colour. Alpha is implicit (always full).
type RGB struct {
	R, G, B uint8
}

// Frame is a 32×8 pixel buffer. Dirty[y][x] is true when the pixel has
// been painted; otherwise Pixels[y][x] is meaningless and should not be
// emitted. The encoder treats undirty pixels as "off" (black).
type Frame struct {
	Pixels [8][32]RGB
	Dirty  [8][32]bool
}

// paintCell sets a single pixel. Out-of-bounds writes are no-ops so callers
// can paint sprites that overhang the matrix without bounds-checking each one.
func paintCell(f *Frame, x, y int, c RGB) {
	if x < 0 || x >= 32 || y < 0 || y >= 8 {
		return
	}
	f.Pixels[y][x] = c
	f.Dirty[y][x] = true
}

// paintRow paints a horizontal run from x0 to x1 inclusive on row y.
func paintRow(f *Frame, x0, x1, y int, c RGB) {
	if y < 0 || y >= 8 {
		return
	}
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		paintCell(f, x, y, c)
	}
}

// paintBitmap paints the lit pixels of a sprite at (ox, oy). Each row of
// the sprite is a string of 'X' (lit) / '.' (transparent) chars.
func paintBitmap(f *Frame, ox, oy int, sprite []string, c RGB) {
	for y, row := range sprite {
		for x, ch := range row {
			if ch == 'X' {
				paintCell(f, ox+x, oy+y, c)
			}
		}
	}
}

// font3x5 maps a rune to its 3-col × 5-row pixel sprite. Each entry is
// exactly 5 strings of exactly 3 chars; 'X' = lit, '.' = transparent.
// Glyphs are based on the classic Picopixel-style 3×5 family.
var font3x5 = map[rune][]string{
	'0': {"XXX", "X.X", "X.X", "X.X", "XXX"},
	'1': {".X.", "XX.", ".X.", ".X.", "XXX"},
	'2': {"XXX", "..X", "XXX", "X..", "XXX"},
	'3': {"XXX", "..X", "XXX", "..X", "XXX"},
	'4': {"X.X", "X.X", "XXX", "..X", "..X"},
	'5': {"XXX", "X..", "XXX", "..X", "XXX"},
	'6': {"XXX", "X..", "XXX", "X.X", "XXX"},
	'7': {"XXX", "..X", "..X", "..X", "..X"},
	'8': {"XXX", "X.X", "XXX", "X.X", "XXX"},
	'9': {"XXX", "X.X", "XXX", "..X", "XXX"},
	'/': {"..X", "..X", ".X.", "X..", "X.."},
	'+': {"...", ".X.", "XXX", ".X.", "..."},
}

// glyph returns the sprite for the given rune, or nil when unsupported.
// Callers that pass user input must check the return value.
func glyph(r rune) []string {
	g, ok := font3x5[r]
	if !ok {
		return nil
	}
	return g
}

// drawDigits paints text at (startX, startY) using the 3×5 font.
// Glyph spacing is 1 px so each character advances startX by 4.
// Unsupported runes are silently skipped (callers should pre-validate).
func drawDigits(f *Frame, text string, startX, startY int, c RGB) {
	x := startX
	for _, ch := range text {
		g := glyph(ch)
		if g != nil {
			paintBitmap(f, x, startY, g, c)
		}
		x += 4 // 3-wide glyph + 1-px spacer
	}
}

// robotNormal is the 10-wide × 6-tall mark painted at cols 0–9, rows 1–6.
// Head top, two 1-px eye holes (cols 2 & 7), arms full-width at row 4
// (protruding to cols 0 & 9), body, and four legs at cols 1, 3, 6, 8.
var robotNormal = []string{
	".XXXXXXXX.", // row 1: head top
	".X.XXXX.X.", // row 2: eyes upper (col 2 & col 7 dark)
	".X.XXXX.X.", // row 3: eyes lower
	"XXXXXXXXXX", // row 4: arms
	".XXXXXXXX.", // row 5: body
	".X.X..X.X.", // row 6: legs
}

// robotError differs only in rows 2–4: 3-px tall chevron eye holes
// (> <) sloping inward, plus matching eye-notches in the arms row.
// The arm protrusions at cols 0 and 9 remain lit.
var robotError = []string{
	".XXXXXXXX.",
	".X.XXXX.X.", // row 2: outer holes (col 2 & 7)
	".XX.XX.XX.", // row 3: apex holes (col 3 & 6)
	"XX.XXXX.XX", // row 4: outer holes (col 2 & 7) + arm protrusions retained
	".XXXXXXXX.",
	".X.X..X.X.",
}

// drawRobot paints the robot sprite at cols 0–9, rows 1–6, using c for lit pixels.
// The "error" state selects the chevron-eye sprite; everything else uses normal.
func drawRobot(f *Frame, state string, c RGB) {
	sprite := robotNormal
	if state == "error" {
		sprite = robotError
	}
	paintBitmap(f, 0, 1, sprite, c)
}

const (
	glassLeft       = 25
	glassRight      = 30
	glassTopRow     = 1
	glassBottomRow  = 5
	glassFillLevels = 4
)

var glassWall = RGB{0xcc, 0xcc, 0xcc}

// drawGlass paints the context-window glass at cols 25–30, rows 1–5.
// If pct is nil the glass is not drawn at all (visually empty space —
// distinguishes from a session reporting 0 %). When non-nil, the outline
// is drawn in glassWall and the interior is filled bottom-up in c.
func drawGlass(f *Frame, pct *int, c RGB) {
	if pct == nil {
		return
	}
	for y := glassTopRow; y < glassBottomRow; y++ {
		paintCell(f, glassLeft, y, glassWall)
		paintCell(f, glassRight, y, glassWall)
	}
	paintRow(f, glassLeft, glassRight, glassBottomRow, glassWall)

	v := *pct
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	var levels int
	switch {
	case v < 1:
		levels = 0
	case v < 25:
		levels = 1
	case v < 50:
		levels = 2
	case v < 75:
		levels = 3
	default:
		levels = 4
	}
	for i := 0; i < levels; i++ {
		y := (glassBottomRow - 1) - i
		for x := glassLeft + 1; x <= glassRight-1; x++ {
			paintCell(f, x, y, c)
		}
	}
}

const (
	barRow   = 7
	barStart = 11
	barEnd   = 31
	barWidth = barEnd - barStart + 1
)

var colorRateBar = RGB{0xff, 0xc1, 0x4d}

// drawRateBar paints the 5h-window bar at row 7, cols 11–31. nil or 0 → no bar.
func drawRateBar(f *Frame, pct *int) {
	if pct == nil || *pct <= 0 {
		return
	}
	v := *pct
	if v > 100 {
		v = 100
	}
	fillLen := (barWidth*v + 50) / 100
	if fillLen < 1 {
		fillLen = 1
	}
	paintRow(f, barStart, barStart+fillLen-1, barRow, colorRateBar)
}

// framePixels extracts the 256-int row-major pixel array from a Frame.
// Shared by frameToCustomApp (1-frame) and pulseFrameToCustomApp
// (2-frame breathe). Undirty cells emit 0 (the encoder's "off" colour).
func framePixels(f *Frame) []int {
	pixels := make([]int, 256)
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			if !f.Dirty[y][x] {
				continue
			}
			c := f.Pixels[y][x]
			pixels[y*32+x] = (int(c.R) << 16) | (int(c.G) << 8) | int(c.B)
		}
	}
	return pixels
}

// frameToCustomApp encodes a Frame as an AWTRIX CustomApp payload using
// one db (draw bitmap) operation. Pixels are emitted row-major as
// 0xRRGGBB ints — undirty pixels emit 0 (black/off).
func frameToCustomApp(f *Frame, lifetimeSeconds int) map[string]any {
	return map[string]any{
		"draw": []any{
			map[string]any{"db": []any{0, 0, 32, 8, framePixels(f)}},
		},
		"lifetime": lifetimeSeconds,
		"duration": lifetimeSeconds,
	}
}

var (
	colorRunning = RGB{0x2e, 0xe8, 0x5e}
	colorWaiting = RGB{0xff, 0xc1, 0x4d}
	colorError   = RGB{0xff, 0x3a, 0x3a}
	colorDone    = RGB{0x4f, 0xa9, 0xff}
	colorWhite   = RGB{0xff, 0xff, 0xff}
)

// pickWinning returns the priority-winning session, its state colour, and
// the total active session count (any non-idle session). When no session
// is active, win is nil. Priority order: waiting > error > running > done.
// Within a tie, the most recently updated session wins.
func pickWinning(sessions []Session) (win *Session, color RGB, total int) {
	var waiting, errored, running, done []*Session
	for i := range sessions {
		s := &sessions[i]
		switch s.State {
		case "waiting":
			waiting = append(waiting, s)
		case "error":
			errored = append(errored, s)
		case "running":
			running = append(running, s)
		case "done":
			done = append(done, s)
		}
	}
	total = len(waiting) + len(errored) + len(running) + len(done)

	pickMostRecent := func(group []*Session) *Session {
		if len(group) == 0 {
			return nil
		}
		best := group[0]
		for _, s := range group[1:] {
			if s.UpdatedAt.After(best.UpdatedAt) {
				best = s
			}
		}
		return best
	}

	switch {
	case len(waiting) > 0:
		return pickMostRecent(waiting), colorWaiting, total
	case len(errored) > 0:
		return pickMostRecent(errored), colorError, total
	case len(running) > 0:
		return pickMostRecent(running), colorRunning, total
	case len(done) > 0:
		return pickMostRecent(done), colorDone, total
	}
	return nil, RGB{}, total
}

// sessionKey is the canonical key for rotation pointer tracking and
// preempt addressing. Stable across coordinator restarts as long as the
// session identity tuple is stable.
func sessionKey(s Session) string {
	return s.Source + "|" + s.Tool + "|" + s.Session
}

// statePriority returns lower values for higher-priority states. Idle is
// never returned by sortedActiveKeys, so the constant for idle is unused
// here but kept for symmetry with the spec's ordering.
func statePriority(state string) int {
	switch state {
	case "waiting":
		return 0
	case "error":
		return 1
	case "running":
		return 2
	case "done":
		return 3
	default:
		return 4 // idle / unknown
	}
}

// sortedActiveKeys returns the canonical keys of non-idle sessions in
// rotation order: state-priority first, then (source, tool, session)
// lexicographically. Stable for a given snapshot.
func sortedActiveKeys(snap Snapshot) []string {
	type entry struct {
		key  string
		prio int
		src  string
		tool string
		sess string
	}
	out := make([]entry, 0, len(snap.Sessions))
	for _, s := range snap.Sessions {
		if s.State == "idle" {
			continue
		}
		out = append(out, entry{
			key:  sessionKey(s),
			prio: statePriority(s.State),
			src:  s.Source,
			tool: s.Tool,
			sess: s.Session,
		})
	}
	slices.SortFunc(out, func(a, b entry) int {
		if a.prio != b.prio {
			return a.prio - b.prio
		}
		if a.src != b.src {
			if a.src < b.src {
				return -1
			}
			return 1
		}
		if a.tool != b.tool {
			if a.tool < b.tool {
				return -1
			}
			return 1
		}
		if a.sess < b.sess {
			return -1
		}
		if a.sess > b.sess {
			return 1
		}
		return 0
	})
	keys := make([]string, len(out))
	for i, e := range out {
		keys[i] = e.key
	}
	return keys
}

// pickRotated advances the rotation pointer. Returns "" on empty input.
// If prev is empty or no longer in keys, returns the first key. Otherwise
// returns the next key with wraparound.
func pickRotated(prev string, keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	idx := slices.Index(keys, prev)
	if idx < 0 {
		return keys[0]
	}
	return keys[(idx+1)%len(keys)]
}

// parseHex parses a "#RRGGBB" string into an RGB. Returns false on malformed
// input; callers should already have validated via isHexColor, so failure
// here indicates a programming bug.
func parseHex(s string) (RGB, bool) {
	if !isHexColor(s) {
		return RGB{}, false
	}
	n, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return RGB{}, false
	}
	return RGB{
		R: uint8(n >> 16),
		G: uint8(n >> 8),
		B: uint8(n),
	}, true
}

// formatXY returns "X/Y", capping at "X/9+" when total exceeds 9.
// idx is 1-based.
func formatXY(idx, total int) string {
	if total <= 9 {
		return itoa(idx) + "/" + itoa(total)
	}
	if idx > 9 {
		idx = 9
	}
	return itoa(idx) + "/9+"
}

// itoa is a small stdlib-only digit-to-string for the count formatter.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return strconv.Itoa(n)
}

// numStart is the left edge of the digit area (1-px gap after the robot).
const numStart = 12

// RenderForCoord composes the AWTRIX CustomApp payload for the
// coordinator's current display state. Returns nil when there is no
// active session (caller skips publish entirely).
//
// pointer: the session key the coordinator wants to display. If empty
// or missing from the snapshot's active set, the first sorted active
// session is chosen instead (fallback for first-publish / pointer
// invalidation).
//
// locked: when true and the chosen session's state is "waiting" or
// "error", emits a 2-frame breathe pulse. Otherwise emits a single
// frame.
func RenderForCoord(snap Snapshot, pointer string, locked bool, lifetimeSeconds int) map[string]any {
	keys := sortedActiveKeys(snap)
	if len(keys) == 0 {
		return nil
	}
	chosen := pointer
	if !slices.Contains(keys, chosen) {
		chosen = keys[0]
	}
	var session *Session
	for i := range snap.Sessions {
		if sessionKey(snap.Sessions[i]) == chosen {
			session = &snap.Sessions[i]
			break
		}
	}
	if session == nil {
		return nil
	}
	idx := slices.Index(keys, chosen) + 1 // 1-based rotation index
	total := len(keys)

	stateColor := colorForState(session.State)
	frameA := composeFrame(*session, idx, total, stateColor)

	if locked && (session.State == "waiting" || session.State == "error") {
		frameB := composeFrame(*session, idx, total, dimRGB(stateColor, 0x66))
		return pulseFrameToCustomApp(frameA, frameB, lifetimeSeconds)
	}
	return frameToCustomApp(&frameA, lifetimeSeconds)
}

// composeFrame paints the standard layout for one session using the
// supplied robot colour. Digits stay source-coloured (or white fallback)
// regardless of robot colour, so digits are stable across the
// brightness pulse. Glass uses the session's state colour directly.
func composeFrame(s Session, idx, total int, robotColor RGB) Frame {
	var f Frame
	drawRobot(&f, s.State, robotColor)

	digitColor := colorWhite
	if s.SourceColor != nil {
		if c, ok := parseHex(*s.SourceColor); ok {
			digitColor = c
		}
	}
	drawDigits(&f, formatXY(idx, total), numStart, 1, digitColor)

	glassFillColor := colorForState(s.State)
	drawGlass(&f, s.ContextPct, glassFillColor)

	drawRateBar(&f, nil) // G.4 plumbs the data; nil for G.1b.
	return f
}

// colorForState returns the state palette colour. Unknown states map to
// white so render never panics on bad input.
func colorForState(state string) RGB {
	switch state {
	case "waiting":
		return colorWaiting
	case "error":
		return colorError
	case "running":
		return colorRunning
	case "done":
		return colorDone
	default:
		return colorWhite
	}
}

// dimRGB scales each channel by scale/0xff (0x66/0xff ≈ 40%).
func dimRGB(c RGB, scale uint8) RGB {
	return RGB{
		R: uint8(int(c.R) * int(scale) / 0xff),
		G: uint8(int(c.G) * int(scale) / 0xff),
		B: uint8(int(c.B) * int(scale) / 0xff),
	}
}

// pulseFrameToCustomApp encodes two frames as a single CustomApp payload.
// AWTRIX cycles between them at the configured frame_duration (500ms),
// producing the breathe pulse without any HTTP traffic. Falls through
// the same db op shape as frameToCustomApp so existing client code (and
// AWTRIX firmware) handles both cases uniformly.
func pulseFrameToCustomApp(a, b Frame, lifetimeSeconds int) map[string]any {
	return map[string]any{
		"draw": []any{
			map[string]any{"db": []any{0, 0, 32, 8, framePixels(&a)}},
			map[string]any{"db": []any{0, 0, 32, 8, framePixels(&b)}},
		},
		"lifetime":       lifetimeSeconds,
		"duration":       lifetimeSeconds,
		"frame_duration": 500,
	}
}
