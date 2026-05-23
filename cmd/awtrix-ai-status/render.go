package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
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

// glassGlyph is the internal font key for the context-window glass pictogram
// (an open-top tumbler with a filled base — a partially-full glass) — the
// trailing glyph on the context-number card. Deliberately NOT 0-shaped: a
// hollow open-top sprite reads as "0" at LED distance, so the base is solid.
const glassGlyph = '⌷'

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
	'%': {"X.X", "..X", ".X.", "X..", "X.X"},
	glassGlyph: {"X.X", "X.X", "X.X", "XXX", "XXX"},
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

// codexSprite is the Codex ">_" mark (10×6), kept pixel-identical to the copy
// in cmd/awtrix-menu/icon.go (guarded by TestCodexSpriteCanonical in both
// packages). 2-px chevron (cols 0–3) + underscore (row 5, cols 5–9).
var codexSprite = []string{
	"XX........",
	".XX.......",
	"..XX......",
	"..XX......",
	".XX.......",
	"XX...XXXXX",
}

// spriteFor selects the 10-wide mark for a session: Codex gets ">_"; Claude
// gets the robot (chevron-eye variant on error).
func spriteFor(s Session) []string {
	if s.Tool == "codex" {
		return codexSprite
	}
	if s.State == "error" {
		return robotError
	}
	return robotNormal
}

func drawRobot(f *Frame, s Session, c RGB) {
	paintBitmap(f, 0, 1, spriteFor(s), c)
}

const (
	glassLeft        = 25
	glassRight       = 30
	glassTopRow      = 1
	glassBottomRow   = 5
	glassInteriorW   = 4  // interior cols 26–29
	glassInteriorH   = 4  // interior rows 1–4
	glassInteriorPix = glassInteriorW * glassInteriorH
)

var glassWall = RGB{0xcc, 0xcc, 0xcc}

// drawGlass paints the context-window glass at cols 25–30, rows 1–5.
// If pct is nil the glass is not drawn at all (visually empty space —
// distinguishes from a session reporting 0 %). When non-nil, the outline is
// drawn in glassWall and the 16 interior pixels (cols 26–29 × rows 1–4) are
// filled bottom-up in c, proportional to pct (≈6 % per pixel — far finer than
// the old 4 row-levels, so e.g. 73 % and 99 % look different). The topmost
// partial row fills center-out (cols 27,28 before 26,29) so the waterline
// reads as a level rather than filling from one edge.
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
	n := (v*glassInteriorPix + 50) / 100 // round(v/100 * 16)
	if n > glassInteriorPix {
		n = glassInteriorPix
	}
	// Center-out column order within a row: 27, 28, 26, 29.
	colOrder := [glassInteriorW]int{glassLeft + 2, glassLeft + 3, glassLeft + 1, glassRight - 1}
	for row := 0; row < glassInteriorH && n > 0; row++ {
		y := (glassBottomRow - 1) - row // 4, 3, 2, 1 (bottom-up)
		k := n
		if k > glassInteriorW {
			k = glassInteriorW
		}
		for i := 0; i < k; i++ {
			paintCell(f, colOrder[i], y, c)
		}
		n -= k
	}
}

const (
	barRow   = 7
	barStart = 11
	barEnd   = 31
	barWidth = barEnd - barStart + 1
)

// drawSessionBar paints one pixel per non-idle session at row 7, starting
// from cols barStart..barEnd. Pixels are coloured by each session's state
// using the existing state-colour palette. Order is priority-first
// (waiting > error > running > done) then (source, tool, session) lex —
// identical to the X/Y rotation order so the leftmost pixel corresponds
// to rotation slot 1/Y. Sessions in state "idle" are excluded. If more
// than barWidth (21) non-idle sessions exist, only the first 21 are
// painted; no special overflow indicator is drawn (the digit area's
// X/9+ truncation already conveys overflow).
func drawSessionBar(f *Frame, sessions []Session) {
	type entry struct {
		prio  int
		src   string
		tool  string
		sess  string
		color RGB
	}
	out := make([]entry, 0, len(sessions))
	for _, s := range sessions {
		if s.State == "idle" {
			continue
		}
		out = append(out, entry{
			prio:  statePriority(s.State),
			src:   s.Source,
			tool:  s.Tool,
			sess:  s.Session,
			color: colorForState(s.State),
		})
	}
	slices.SortFunc(out, func(a, b entry) int {
		if a.prio != b.prio {
			return a.prio - b.prio
		}
		if a.src != b.src {
			return strings.Compare(a.src, b.src)
		}
		if a.tool != b.tool {
			return strings.Compare(a.tool, b.tool)
		}
		return strings.Compare(a.sess, b.sess)
	})
	for i, e := range out {
		if i >= barWidth {
			break
		}
		paintCell(f, barStart+i, barRow, e.color)
	}
}

// drawRateBar paints the 5h rate-limit window as a horizontal fill bar at
// row 7, cols 11–31 — the same footprint as the session-count bar it replaces
// when the rate-bottom-bar toggle is on. The fill length is proportional to
// pct (a minimum of 1 px for any non-zero rate, so low usage stays visible);
// pct <= 0 paints nothing. The whole fill is a single colour c (the caller
// passes rateColor(pct): green <70 / amber / red >=90).
func drawRateBar(f *Frame, pct int, c RGB) {
	if pct <= 0 {
		return
	}
	if pct > 100 {
		pct = 100
	}
	fillLen := (barWidth*pct + 50) / 100 // round(pct/100 * 21)
	if fillLen < 1 {
		fillLen = 1
	}
	paintRow(f, barStart, barStart+fillLen-1, barRow, c)
}

// framePixels extracts the 256-int row-major pixel array from a Frame.
// Used by frameToCustomApp to serialise a Frame into the AWTRIX db
// (drawBMP) pixel array. Undirty cells emit 0 (the encoder's "off" colour).
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
		"prio":     true,
		"force":    true,
	}
}

var (
	colorRunning = RGB{0x2e, 0xe8, 0x5e}
	colorWaiting = RGB{0xff, 0xc1, 0x4d}
	colorError   = RGB{0xff, 0x3a, 0x3a}
	colorDone    = RGB{0x4f, 0xa9, 0xff}
	colorWhite   = RGB{0xff, 0xff, 0xff}
)

// Card identifies which readout the number slot shows for the current
// session: cardXY is the rotation index "X/Y", cardRate is the 5h rate-limit
// "NN%", cardTool is the scrolling activity detail, cardCtx is the context
// window percent "NN⌷".
const (
	cardXY = iota
	cardRate
	cardTool
	cardCtx
)

// availableCards returns the cards this session offers, in rotation order:
// X/Y always; the rate card when RateWindowPct is set; the tool card only for
// a running session that carries an Activity string. The rotation cursor
// indexes this slice, so a session can have X/Y+tool without a rate card.
func availableCards(s Session) []int {
	cards := []int{cardXY}
	if s.RateWindowPct != nil {
		cards = append(cards, cardRate)
	}
	if s.ContextNumber && s.ContextPct != nil {
		cards = append(cards, cardCtx)
	}
	if s.State == "running" && s.Activity != "" {
		cards = append(cards, cardTool)
	}
	return cards
}

func cardsForSession(s Session) int { return len(availableCards(s)) }

// rateText renders a 5h-rate percent as "NN%". Clamped to 0..99 so the
// 3-glyph value always fits cols 12–22 (before the glass at col 25); the
// red threshold colour already signals a maxed window, so 99 vs 100 is
// immaterial on an ambient display.
func rateText(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 99 {
		pct = 99
	}
	return itoa(pct) + "%"
}

// ctxText renders a context-window percent as "NN" + the glass pictogram.
// Clamped 0..99 so the 3-glyph value fits the number slot (the glass at cols
// 25-30 conveys 100% anyway).
func ctxText(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 99 {
		pct = 99
	}
	return itoa(pct) + string(glassGlyph)
}

// rateColor threshold-colours the rate readout, matching Claude Code's
// statusline convention: <70 green, 70–89 amber, >=90 red.
func rateColor(pct int) RGB {
	switch {
	case pct >= 90:
		return colorError
	case pct >= 70:
		return colorWaiting
	default:
		return colorRunning
	}
}

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
// preempt addressing. Delegates to Session.key (slash-delimited form
// also used by App.sessions and DeleteRequest.key) so handlers and the
// coordinator agree on one string for one session — without that
// alignment, priorState() lookups and delete-while-locked release
// silently miss.
func sessionKey(s Session) string {
	return s.key()
}

// sessionByKey returns the session in snap whose canonical key matches,
// or the zero Session when absent. Shared by the coordinator's rotation
// advance and RenderForCoord so both agree on the session a key names.
func sessionByKey(snap Snapshot, key string) Session {
	for i := range snap.Sessions {
		if sessionKey(snap.Sessions[i]) == key {
			return snap.Sessions[i]
		}
	}
	return Session{}
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

// detailPayload builds a robot(db) + AWTRIX-native-text payload. blink=true is
// the WAIT/ERR fallback (static, blinking). blink=false is the activity detail
// (firmware shows it static when it fits, scrolls when it overflows). center is
// always false so textOffset is the literal start column (cols 11-31), clear of
// the 10-wide robot.
func detailPayload(s Session, text, hexColor string, blink bool, lifetimeSeconds int) map[string]any {
	pixels := composeRobotPixels(s, colorForState(s.State))
	p := map[string]any{
		"draw":       []any{map[string]any{"db": []any{0, 0, robotWidth, 8, pixels}}},
		"text":       text,
		"color":      hexColor,
		"textOffset": 11,
		"center":     false,
		"duration":   lifetimeSeconds,
		"lifetime":   lifetimeSeconds,
		"prio":       true,
		"force":      true,
	}
	if blink {
		p["blinkText"] = 500
		p["noScroll"] = true
	}
	return p
}

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
// "error", a narrow 10-col db (just the robot sprite) is emitted with
// text+blinkText positioned to the right via textOffset, so AWTRIX
// firmware animates the attention indicator natively in the area the
// bitmap leaves clear. A full-width draw would paint zeros across the
// right side of the matrix and clobber the text underneath — verified
// empirically against device 0.98.
func RenderForCoord(snap Snapshot, pointer string, card int, locked bool, lifetimeSeconds int) map[string]any {
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

	if locked && (session.State == "waiting" || session.State == "error") {
		if session.Activity != "" {
			return detailPayload(*session, session.Activity, stateHex(session.State), false, lifetimeSeconds)
		}
		label, hex := attentionLabelAndColor(session.State)
		return detailPayload(*session, label, hex, true, lifetimeSeconds)
	}

	cards := availableCards(*session)
	ci := card
	if ci < 0 || ci >= len(cards) {
		ci = 0
	}
	selected := cards[ci]
	if selected == cardTool {
		return detailPayload(*session, session.Activity, stateHex(session.State), false, lifetimeSeconds)
	}
	frame := composeFrame(*session, idx, total, selected, stateColor, snap.Sessions)
	return frameToCustomApp(&frame, lifetimeSeconds)
}

// robotWidth is the horizontal extent of the locked-frame bitmap and
// the idle-dim bitmap. The remaining 32-robotWidth columns are left
// blank so AWTRIX renders the blinking attention text in that area
// (locked path) or stay dark (idle path). Set to 10 — matches the
// rotation-frame sprite so all 4 legs and both arm protrusions are
// preserved. AWTRIX default 3×5 font: "WAIT" ≈ 15 px, "ERR" ≈ 10 px,
// both fit in the remaining 22 cols (10-31) with noScroll:true.
const robotWidth = 10

// composeRobotPixels paints just the robot sprite into a tight
// robotWidth×8 = 80-int pixel array. Uses the same 10-wide
// robot{Normal,Error} sprites as the rotation frame so all 4 legs and
// both arm protrusions are preserved. Called by RenderForCoord (locked
// attention path) and RenderIdleFrame (dim-white countdown).
func composeRobotPixels(s Session, robotColor RGB) []int {
	sprite := spriteFor(s)
	var f Frame
	paintBitmap(&f, 0, 1, sprite, robotColor)
	pixels := make([]int, robotWidth*8)
	for y := 0; y < 8; y++ {
		for x := 0; x < robotWidth; x++ {
			if !f.Dirty[y][x] {
				continue
			}
			c := f.Pixels[y][x]
			pixels[y*robotWidth+x] = (int(c.R) << 16) | (int(c.G) << 8) | int(c.B)
		}
	}
	return pixels
}

// composeFrame paints the standard layout for one session using the
// supplied robot colour. Digits stay source-coloured (or white fallback)
// regardless of robot colour. Glass uses the session's state colour
// directly. Row 7 receives the session-count bar drawn from the full
// active-session list `sessions` — see drawSessionBar.
func composeFrame(s Session, idx, total, card int, robotColor RGB, sessions []Session) Frame {
	var f Frame
	drawRobot(&f, s, robotColor)

	switch {
	case card == cardRate && s.RateWindowPct != nil:
		pct := *s.RateWindowPct
		drawDigits(&f, rateText(pct), numStart, 1, rateColor(pct))
	case card == cardCtx && s.ContextPct != nil:
		pct := *s.ContextPct
		// Deliberately reuses rateColor's green/amber/red fullness thresholds
		// (70/90) — same "how full" semantics. Split into a ctxColor if context
		// ever needs different thresholds than the rate window.
		drawDigits(&f, ctxText(pct), numStart, 1, rateColor(pct))
	default:
		digitColor := colorWhite
		if s.SourceColor != nil {
			if c, ok := parseHex(*s.SourceColor); ok {
				digitColor = c
			}
		}
		drawDigits(&f, formatXY(idx, total), numStart, 1, digitColor)
	}

	glassFillColor := colorForState(s.State)
	drawGlass(&f, s.ContextPct, glassFillColor)

	drawSessionBar(&f, sessions)
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

// attentionLabelAndColor returns the short blinking label and its
// hex colour for the locked attention state. Only the firmware-known
// attention states ("waiting" → "WAIT", "error" → "ERR") have defined
// labels; other states fall back to "WAIT" but the call site already
// guards against entry with state ∉ {waiting, error}.
func attentionLabelAndColor(state string) (string, string) {
	if state == "error" {
		return "ERR", fmt.Sprintf("#%02X%02X%02X", colorError.R, colorError.G, colorError.B)
	}
	return "WAIT", fmt.Sprintf("#%02X%02X%02X", colorWaiting.R, colorWaiting.G, colorWaiting.B)
}

// stateHex returns the "#RRGGBB" string for a state's palette colour, for use
// as the AWTRIX text colour in detail payloads.
func stateHex(state string) string {
	c := colorForState(state)
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// idleDimWhite is the robot colour during the idle-restore countdown:
// roughly 40% brightness (0x66 / 0xff) — a clear "the display is alive
// but no work is happening" cue, distinct from the bright state colours
// used while sessions are active.
var idleDimWhite = RGB{0x66, 0x66, 0x66}

// RenderIdleFrame returns the dimmed-robot payload emitted during the
// G.2 idle-restore countdown. No digits, no glass, no text — the robot
// dims to ~40% white and the rest of the matrix stays dark, leaving an
// unambiguous "AI idle" signal that's also visually distinct from the
// active rotation frames. Includes prio+force+lifetime so AWTRIX keeps
// holding the slot until the countdown elapses and we stop publishing.
func RenderIdleFrame(lifetimeSeconds int) map[string]any {
	pixels := composeRobotPixels(Session{State: "idle"}, idleDimWhite)
	return map[string]any{
		"draw": []any{
			map[string]any{"db": []any{0, 0, robotWidth, 8, pixels}},
		},
		"lifetime": lifetimeSeconds,
		"duration": lifetimeSeconds,
		"prio":     true,
		"force":    true,
	}
}
