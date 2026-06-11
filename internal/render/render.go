package render

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
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

// Session holds the current state of a single AI session as received via the
// status endpoint.
type Session struct {
	Source         string  `json:"source"`
	Tool           string  `json:"tool"`
	Session        string  `json:"session"`
	State          string  `json:"state"`
	Message        string  `json:"message"`
	TokensToday    int64   `json:"tokens_today,omitempty"`
	ContextPct     *int    `json:"context_pct,omitempty"`
	SourceColor    *string `json:"source_color,omitempty"`
	RateWindowPct  *int    `json:"rate_window_pct,omitempty"`
	Activity       string  `json:"activity,omitempty"`
	ContextNumber  bool    `json:"context_number,omitempty"`
	RateBottomBar  bool    `json:"rate_bottom_bar,omitempty"`
	RateResetAt    int64   `json:"rate_reset_at,omitempty"`
	RateReset      bool    `json:"rate_reset,omitempty"`
	RateResetLabel string  `json:"rate_reset_label,omitempty"`
	// SourceCard / SessionBar are *bool so a producer that predates them (nil)
	// keeps the element ON — absent must never regress the display.
	SourceCard *bool     `json:"source_card,omitempty"`
	SessionBar *bool     `json:"session_bar,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Key returns the canonical slash-delimited key for this session.
func (s Session) Key() string {
	return s.Source + "/" + s.Tool + "/" + s.Session
}

// Snapshot is a point-in-time view of all sessions plus the computed Render.
type Snapshot struct {
	Now      time.Time `json:"now"`
	Sessions []Session `json:"sessions"`
	Render   Render    `json:"render"`
}

// Render is the computed summary of the current session set (text/color/counters).
type Render struct {
	Text        string `json:"text"`
	Color       string `json:"color"`
	Waiting     int    `json:"waiting"`
	Running     int    `json:"running"`
	Errors      int    `json:"errors"`
	Done        int    `json:"done"`
	ActiveTotal int    `json:"active_total"`
	Message     string `json:"message,omitempty"`
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

// resetGlyph is the internal font key for the rate-reset hourglass pictogram
// (a symmetric I-beam: wide top/bottom plates, thin sand stream) — the trailing
// glyph on the reset-countdown card. Distinct from the digits and the context
// tumbler glassGlyph.
const resetGlyph = '⧗'

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
	'-': {"...", "...", "XXX", "...", "..."},
	'%': {"X.X", "..X", ".X.", "X..", "X.X"},
	// Degree sign for the weather widget's temperature readout: a small ring in
	// the top two rows (reads as "°" beside the digits, distinct from '0').
	'°': {"XX.", "XX.", "...", "...", "..."},
	// Usage-widget glyphs. ':' is 1-wide (tight clock colon); the letters are
	// the 5h/7d unit suffixes + the per-model OP/SO markers. The tight clock is
	// painted by drawClockInto (custom per-glyph advance), so the 1-wide ':'
	// never reaches drawDigits' fixed +4 advance.
	':':        {".", "X", ".", "X", "."},
	'h':        {"X..", "X..", "XXX", "X.X", "X.X"},
	'd':        {"..X", "..X", "XXX", "X.X", "XXX"},
	'O':        {"XXX", "X.X", "X.X", "X.X", "XXX"},
	'P':        {"XXX", "X.X", "XXX", "X..", "X.."},
	'S':        {"XXX", "X..", "XXX", "..X", "XXX"},
	glassGlyph: {"X.X", "X.X", "X.X", "XXX", "XXX"},
	resetGlyph: {"XXX", ".X.", ".X.", ".X.", "XXX"},
	// Source-name card letters (A-Z minus the pre-existing O/P/S above).
	'A': {"XXX", "X.X", "XXX", "X.X", "X.X"},
	'B': {"XX.", "X.X", "XX.", "X.X", "XX."},
	'C': {"XXX", "X..", "X..", "X..", "XXX"},
	'D': {"XX.", "X.X", "X.X", "X.X", "XX."},
	'E': {"XXX", "X..", "XXX", "X..", "XXX"},
	'F': {"XXX", "X..", "XXX", "X..", "X.."},
	'G': {"XXX", "X..", "X.X", "X.X", "XXX"},
	'H': {"X.X", "X.X", "XXX", "X.X", "X.X"},
	'I': {"XXX", ".X.", ".X.", ".X.", "XXX"},
	'J': {"..X", "..X", "..X", "X.X", "XXX"},
	'K': {"X.X", "X.X", "XX.", "X.X", "X.X"},
	'L': {"X..", "X..", "X..", "X..", "XXX"},
	'M': {"XXX", "XXX", "X.X", "X.X", "X.X"},
	'N': {"X.X", "XXX", "X.X", "X.X", "X.X"},
	'Q': {"XXX", "X.X", "X.X", "XXX", "..X"},
	'R': {"XX.", "X.X", "XX.", "X.X", "X.X"},
	'T': {"XXX", ".X.", ".X.", ".X.", ".X."},
	'U': {"X.X", "X.X", "X.X", "X.X", "XXX"},
	'V': {"X.X", "X.X", "X.X", "X.X", ".X."},
	'W': {"X.X", "X.X", "XXX", "XXX", "X.X"},
	'X': {"X.X", "X.X", ".X.", "X.X", "X.X"},
	'Y': {"X.X", "X.X", ".X.", ".X.", ".X."},
	'Z': {"XXX", "..X", ".X.", "X..", "XXX"},
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
// in the retired Go menu's icon renderer (guarded by TestCodexSpriteCanonical).
// 2-px chevron (cols 0–3) + underscore (row 5, cols 5–9).
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
	glassInteriorW   = 4 // interior cols 26–29
	glassInteriorH   = 4 // interior rows 1–4
	glassInteriorPix = glassInteriorW * glassInteriorH
)

var glassWall = RGB{0xcc, 0xcc, 0xcc}

// drawGlass paints the context-window glass at cols 25–30, rows 1–5.
// If pct is nil the glass is not drawn at all (visually empty space —
// distinguishes from a session reporting 0 %). When non-nil, the outline is
// drawn in glassWall and the 16 interior pixels (cols 26–29 × rows 1–4) are
// filled bottom-up in c, proportional to pct (≈6 % per pixel — far finer than
// the old 4 row-levels, so e.g. 73 % and 99 % look different). The topmost
// partial row fills left-to-right (cols 26→29).
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
	// Left-to-right column order within a row: 26, 27, 28, 29.
	colOrder := [glassInteriorW]int{glassLeft + 1, glassLeft + 2, glassLeft + 3, glassRight - 1}
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
// (waiting > error > running > done) then (source, tool, session) lex.
// Sessions in state "idle" are excluded. If more than barWidth (21)
// non-idle sessions exist, only the first 21 are painted; overflow is
// simply not indicated.
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

// drawRateBar paints the 5h rate-limit window as the usage-widget-style dimmed
// threshold bar: content cols 8–31, row 7, fill = round(24*pct/100) in
// dimThreshold(pct) over a usageTrack background — visually identical to the
// usage apps' bars. The colour arg is retained for signature stability but
// ignored (threshold colour is derived from pct).
func drawRateBar(f *Frame, pct int, _ RGB) {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	for i, c := range usageBarPixels(pct) {
		paintCell(f, 8+i, barRow, c)
	}
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

// cardNone is passed to ComposeFrame when no cards are available — the number
// slot is left blank (icon/glass/bar still render). The default branch in
// ComposeFrame's switch handles any unrecognised value including cardNone.
const cardNone = -1

// Card identifies which readout the number slot shows for the current
// session: cardSource is the source-name card, cardRate is the 5h rate-limit
// "NN%", cardTool is the scrolling activity detail, cardCtx is the context
// window percent "NN⌷".
const (
	cardSource = iota // source-name card (replaces the old X/Y rotation card)
	cardRate
	cardTool
	cardCtx
	cardReset
)

func sourceCardEnabled(s Session) bool { return s.SourceCard == nil || *s.SourceCard }
func sessionBarEnabled(s Session) bool { return s.SessionBar == nil || *s.SessionBar }

// sourceCardText uppercases and truncates a source name to the 4 glyphs that
// fit the drawn text area (cols 9-23 = 15 px = 4×4−1; full-frame db apps
// cannot scroll native text, so longer names are cut, not scrolled).
func sourceCardText(source string) string {
	up := strings.ToUpper(source)
	r := []rune(up)
	if len(r) > 4 {
		r = r[:4]
	}
	return string(r)
}

// AvailableCards returns the cards this session offers, in rotation order.
// May return an empty slice (every card disabled/data-absent): the frame then
// shows icon + glass + bar with a blank number slot.
func AvailableCards(s Session) []int {
	var cards []int
	if sourceCardEnabled(s) && s.Source != "" {
		cards = append(cards, cardSource)
	}
	if s.RateWindowPct != nil {
		cards = append(cards, cardRate)
	}
	if s.ContextNumber && s.ContextPct != nil {
		cards = append(cards, cardCtx)
	}
	if s.RateReset && s.RateResetAt > 0 {
		cards = append(cards, cardReset)
	}
	if s.State == "running" && s.Activity != "" {
		cards = append(cards, cardTool)
	}
	return cards
}

func CardsForSession(s Session) int { return len(AvailableCards(s)) }

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

// resetText renders the time until the 5h rate-limit window resets as ceil-hours
// (0..9) + the hourglass glyph, with an urgency colour: amber in the final hour
// (remaining < 1h), green otherwise. remaining is clamped to >=0, so a stale
// past timestamp renders "0" until the next post carries the next window.
func resetText(resetAt int64, now time.Time) (string, RGB) {
	remaining := resetAt - now.Unix()
	if remaining < 0 {
		remaining = 0
	}
	hours := int((remaining + 3599) / 3600) // ceil to whole hours
	if hours > 9 {
		hours = 9
	}
	color := colorRunning
	if remaining < 3600 {
		color = colorWaiting
	}
	return itoa(hours) + string(resetGlyph), color
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

// PickWinning returns the priority-winning session, its state colour, and
// the total active session count (any non-idle session). When no session
// is active, win is nil. Priority order: waiting > error > running > done.
// Within a tie, the most recently updated session wins.
func PickWinning(sessions []Session) (win *Session, color RGB, total int) {
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
	return s.Key()
}

// SessionByKey returns the session in snap whose canonical key matches,
// or the zero Session when absent. Shared by the coordinator's rotation
// advance and RenderForCoord so both agree on the session a key names.
func SessionByKey(snap Snapshot, key string) Session {
	for i := range snap.Sessions {
		if sessionKey(snap.Sessions[i]) == key {
			return snap.Sessions[i]
		}
	}
	return Session{}
}

// statePriority returns lower values for higher-priority states. Idle is
// never returned by SortedActiveKeys, so the constant for idle is unused
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

// SortedActiveKeys returns the canonical keys of non-idle sessions in
// rotation order: state-priority first, then (source, tool, session)
// lexicographically. Stable for a given snapshot.
func SortedActiveKeys(snap Snapshot) []string {
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

// PickRotated advances the rotation pointer. Returns "" on empty input.
// If prev is empty or no longer in keys, returns the first key. Otherwise
// returns the next key with wraparound.
func PickRotated(prev string, keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	idx := slices.Index(keys, prev)
	if idx < 0 {
		return keys[0]
	}
	return keys[(idx+1)%len(keys)]
}

// isHexColor reports whether s is a 7-char string of the form "#RRGGBB"
// with lowercase or uppercase hex digits.
func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
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

// itoa is a small stdlib-only digit-to-string for the count formatter.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return strconv.Itoa(n)
}

// numStart is the left edge of the digit area (1-px gap after the 8×8 icon).
const numStart = 9

// detailPayload builds a robot(db) + AWTRIX-native-text payload. blink=true is
// the WAIT/ERR fallback (static, blinking). blink=false is the activity detail
// (firmware shows it static when it fits, scrolls when it overflows). center is
// always false so textOffset is the literal start column (cols 11-31), clear of
// the 10-wide robot.
func detailPayload(s Session, text, hexColor string, blink bool, lifetimeSeconds int) map[string]any {
	pixels := composeToolIconPixels(s, iconBodyColor(s), colorForState(s.State))
	p := map[string]any{
		"draw":       []any{map[string]any{"db": []any{0, 0, 8, 8, pixels}}},
		"text":       text,
		"color":      hexColor,
		"textOffset": 9,
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
	keys := SortedActiveKeys(snap)
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
	if locked && (session.State == "waiting" || session.State == "error") {
		if session.Activity != "" {
			return detailPayload(*session, session.Activity, stateHex(session.State), false, lifetimeSeconds)
		}
		label, hex := attentionLabelAndColor(session.State)
		return detailPayload(*session, label, hex, true, lifetimeSeconds)
	}

	cards := AvailableCards(*session)
	selected := cardNone // no cards: blank number slot (icon/glass/bar still render)
	if len(cards) > 0 {
		ci := card
		if ci < 0 || ci >= len(cards) {
			ci = 0
		}
		selected = cards[ci]
	}
	if selected == cardTool {
		return detailPayload(*session, session.Activity, stateHex(session.State), false, lifetimeSeconds)
	}
	frame := ComposeFrame(*session, selected, snap.Sessions, snap.Now)
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

// ComposeFrame paints the standard layout for one session. The icon body
// (cols 0-7) is painted in the session's source colour (s.SourceColor), or
// iconNeutral when absent/invalid, so each machine has a persistent identity
// colour. The inner feature — Claude eye sockets or the Codex "_" cursor —
// is painted in the state colour (green/amber/red/blue) so activity is always
// readable. Card text colours are per-card (source = source colour or white,
// rate/ctx = threshold colour). Glass uses the state colour. Row 7 receives
// either the rate bar (when RateBottomBar is set and data is present), the
// session-count bar (when sessionBarEnabled), or nothing.
func ComposeFrame(s Session, card int, sessions []Session, now time.Time) Frame {
	var f Frame
	drawToolIcon8(&f, s, iconBodyColor(s), colorForState(s.State))

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
	case card == cardReset && s.RateResetAt > 0:
		if s.RateResetLabel != "" {
			// New design: precise HH:MM reset time as a tight-colon clock (white
			// digits + dimmed colon), reusing the usage widget's drawClockInto.
			// The host-local label is posted by the statusline path.
			drawClockInto(&f, s.RateResetLabel, numStart)
		} else {
			// Fallback (e.g. Codex, which posts no label): ceil-hours hourglass.
			text, col := resetText(s.RateResetAt, now)
			drawDigits(&f, text, numStart, 1, col)
		}
	case card == cardSource && s.Source != "":
		digitColor := colorWhite
		if s.SourceColor != nil {
			if c, ok := parseHex(*s.SourceColor); ok {
				digitColor = c
			}
		}
		drawDigits(&f, sourceCardText(s.Source), numStart, 1, digitColor)
	default:
		// card == -1 (no cards available) or data went missing: blank number slot.
	}

	glassFillColor := colorForState(s.State)
	drawGlass(&f, s.ContextPct, glassFillColor)

	if s.RateBottomBar && s.RateWindowPct != nil {
		pct := *s.RateWindowPct
		drawRateBar(&f, pct, rateColor(pct))
	} else if sessionBarEnabled(s) {
		drawSessionBar(&f, sessions)
	}
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
	// Idle dim deliberately overrides identity: the dim-white colour covers both
	// body and overlay so the icon reads as "no active work" regardless of source.
	pixels := composeToolIconPixels(Session{State: "idle"}, idleDimWhite, idleDimWhite)
	return map[string]any{
		"draw": []any{
			map[string]any{"db": []any{0, 0, 8, 8, pixels}},
		},
		"lifetime": lifetimeSeconds,
		"duration": lifetimeSeconds,
		"prio":     true,
		"force":    true,
	}
}

// toolIcon8 returns the 8×8 tool sprite for a session: codex chevron, else the
// Claude robot-face. Reuses the usage widget icons so the whole display shares
// one icon set.
func toolIcon8(s Session) []string {
	if s.Tool == "codex" {
		return usageIconCodex
	}
	return usageIconClaude
}

// iconNeutral is the icon body when no (valid) source colour is configured —
// the state channel lives in the eye/cursor overlay, so the body must never
// fall back to a state colour.
var iconNeutral = RGB{0xcc, 0xcc, 0xcc}

// claudeEyes8 lights the robot-face eye sockets (the 2×2-px holes of
// usageIconClaude, rows 2-3 × cols 2,5) in the state colour.
var claudeEyes8 = []string{
	"........",
	"........",
	"..X..X..",
	"..X..X..",
	"........",
	"........",
	"........",
	"........",
}

// codexCursor8 is the "_" cursor of usageIconCodex (row 6, cols 3-6); painted
// after the body it overrides those pixels with the state colour.
var codexCursor8 = []string{
	"........",
	"........",
	"........",
	"........",
	"........",
	"........",
	"...XXXX.",
	"........",
}

func iconBodyColor(s Session) RGB {
	if s.SourceColor != nil {
		if c, ok := parseHex(*s.SourceColor); ok {
			return c
		}
	}
	return iconNeutral
}

func iconOverlay8(s Session) []string {
	if s.Tool == "codex" {
		return codexCursor8
	}
	return claudeEyes8
}

// drawToolIcon8 paints the 8×8 tool icon at cols 0-7. body is the identity
// colour (source colour or neutral) for the icon sprite; feature is the state
// colour painted over the inner detail (Claude eye sockets / Codex "_" cursor).
func drawToolIcon8(f *Frame, s Session, body, feature RGB) {
	paintBitmap(f, 0, 0, toolIcon8(s), body)
	paintBitmap(f, 0, 0, iconOverlay8(s), feature)
}

// composeToolIconPixels paints just the 8×8 icon into a tight 8×8 = 64-int pixel
// array (for the locked-attention / idle db, leaving cols 8-31 clear for native
// text). body is the identity colour; feature is the state colour for the
// eye/cursor overlay.
func composeToolIconPixels(s Session, body, feature RGB) []int {
	var f Frame
	drawToolIcon8(&f, s, body, feature)
	px := make([]int, 64)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if f.Dirty[y][x] {
				cc := f.Pixels[y][x]
				px[y*8+x] = (int(cc.R) << 16) | (int(cc.G) << 8) | int(cc.B)
			}
		}
	}
	return px
}
