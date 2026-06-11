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

// UsageView is the per-tool account-usage data the usage card renders.
// The coordinator builds one per tool from the UsageStore (endpoint data
// preferred, statusline fallback) and only when the tool's 5h window is
// over the configured threshold — render never sees a below-threshold view.
type UsageView struct {
	FiveHourPct int
	ResetLabel  string // host-local "HH:MM"; "" → hourglass fallback from ResetAt
	ResetAt     int64  // unix; used only when ResetLabel is ""
	SevenDayPct *int   // nil when the 7d window is unknown
	Models      []ModelUsage
}

// ModelUsage is one per-model usage face ("OP" opus / "SO" sonnet).
type ModelUsage struct {
	Marker string // exactly two font3x5 glyphs
	Pct    int
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
// session. cardSource is the source-name card; cardTool the scrolling
// activity detail; the cardUsage* family is the account-usage card —
// present only when the coordinator passes a UsageView (5h window over
// the configured threshold).
const (
	cardSource      = iota // source-name card
	cardTool               // scrolling activity detail
	cardUsage5h            // 5h window: reset clock (rate-bar mode) or "NN%" (otherwise)
	cardUsageReset         // 5h reset clock when the bar is NOT in rate mode
	cardUsage7d            // 7-day window: "7dNN"
	cardUsageModelA        // per-model faces (Models[0] / Models[1])
	cardUsageModelB
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
// u is the session tool's usage view (nil = below threshold / widget off /
// no data): without it no usage faces appear. May return an empty slice —
// the frame then shows icon + glass + bar with a blank number slot.
func AvailableCards(s Session, u *UsageView) []int {
	var cards []int
	if sourceCardEnabled(s) && s.Source != "" {
		cards = append(cards, cardSource)
	}
	if u != nil {
		cards = append(cards, cardUsage5h)
		if !s.RateBottomBar && (u.ResetLabel != "" || u.ResetAt > 0) {
			// Pct occupies the 5h face, so the reset clock needs its own card.
			cards = append(cards, cardUsageReset)
		}
		if u.SevenDayPct != nil {
			cards = append(cards, cardUsage7d)
		}
		if len(u.Models) > 0 {
			cards = append(cards, cardUsageModelA)
		}
		if len(u.Models) > 1 {
			cards = append(cards, cardUsageModelB)
		}
	}
	if s.State == "running" && s.Activity != "" {
		cards = append(cards, cardTool)
	}
	return cards
}

func CardsForSession(s Session, u *UsageView) int { return len(AvailableCards(s, u)) }

// rateText renders a 5h-rate percent as "NN%". Clamped to 0..99 so the
// 3-glyph value always fits cols 9–19 (before the glass at col 25); the
// red threshold colour already signals a maxed window, so 99 vs 100 is
// immaterial on an ambient display. Used by the usage-card faces.
func rateText(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 99 {
		pct = 99
	}
	return itoa(pct) + "%"
}

// resetText renders the time until the 5h rate-limit window resets as ceil-hours
// (0..9) + the hourglass glyph, with an urgency colour: amber in the final hour
// (remaining < 1h), green otherwise. remaining is clamped to >=0, so a stale
// past timestamp renders "0" until the next post carries the next window.
// Used by the usage-card faces.
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

// drawUsageClock paints the 5h reset readout: the host-local HH:MM tight
// clock when the label is known, else the ceil-hours hourglass (codex, or
// statusline data without a label).
func drawUsageClock(f *Frame, u *UsageView, now time.Time) {
	if u.ResetLabel != "" {
		drawClockInto(f, u.ResetLabel, numStart)
		return
	}
	text, col := resetText(u.ResetAt, now)
	drawDigits(f, text, numStart, 1, col)
}

// drawUnitPctFace paints a two-glyph gray unit ("7d", "OP", "SO") followed by
// a clamped 2-digit percent in the threshold colour — the "7d42" face shape.
func drawUnitPctFace(f *Frame, unit string, pct int) {
	drawDigits(f, unit, numStart, 1, usageGray)
	drawDigits(f, pctDigits(pct), numStart+8, 1, rateColor(pct))
}

// pctDigits is rateText without the % sign (the unit glyphs already say
// what the number is): clamped 0..99 so it always fits two glyphs.
func pctDigits(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 99 {
		pct = 99
	}
	return itoa(pct)
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

// detailPayload builds an 8×8 icon (db) + AWTRIX-native-text payload. blink=true
// is the WAIT/ERR attention label (blinking; scrolls when the label overflows the
// free columns). blink=false is the activity detail (firmware shows it static when
// it fits, scrolls when it overflows). center is always false so textOffset is the
// literal start column (cols 9-31), clear of the 8-wide icon.
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
		// "WAIT"/"ERR" alone fit the 23 free cols (9-31); with a source name appended
		// the firmware must be allowed to scroll (~4 px/char native font).
		if len(text) <= 5 {
			p["noScroll"] = true
		}
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
//
// usage: per-tool account usage views (keyed by tool name, e.g. "claude").
// A nil map is valid — cards that need usage data simply won't appear.
func RenderForCoord(snap Snapshot, pointer string, card int, locked bool, lifetimeSeconds int, usage map[string]*UsageView) map[string]any {
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
		// Attention names WHO/WHERE (tool icon + source), not which tool call —
		// activity detail no longer substitutes here (2026-06-11 redesign).
		label, hex := attentionLabelAndColor(session.State)
		if session.Source != "" {
			label += " " + strings.ToUpper(session.Source)
		}
		return detailPayload(*session, label, hex, true, lifetimeSeconds)
	}

	// Resolve the per-tool usage view; nil map access is safe in Go.
	u := usage[session.Tool]

	cards := AvailableCards(*session, u)
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
	frame := ComposeFrame(*session, selected, u, snap.Sessions, snap.Now)
	return frameToCustomApp(&frame, lifetimeSeconds)
}

// ComposeFrame paints the standard layout for one session. The icon body
// (cols 0-7) is painted in the session's source colour (s.SourceColor), or
// iconNeutral when absent/invalid, so each machine has a persistent identity
// colour. The inner feature — Claude eye sockets or the Codex "_" cursor —
// is painted in the state colour (green/amber/red/blue) so activity is always
// readable. Card text colours are per-card: source = source colour or white;
// 5h/7d/model percent = threshold colour (green <70 / amber 70–89 / red ≥90);
// reset clock = white digits with a dimmed colon; hourglass fallback = urgency
// colour (amber in the final hour, green otherwise). Glass uses the state
// colour. Row 7 receives either the rate bar (when RateBottomBar is set and
// data is present), the session-count bar (when sessionBarEnabled), or nothing.
func ComposeFrame(s Session, card int, u *UsageView, sessions []Session, now time.Time) Frame {
	var f Frame
	drawToolIcon8(&f, s, iconBodyColor(s), colorForState(s.State))

	switch {
	case card == cardUsage5h && u != nil:
		if s.RateBottomBar {
			drawUsageClock(&f, u, now) // pct lives on the bar; slot shows the clock
		} else {
			drawDigits(&f, rateText(u.FiveHourPct), numStart, 1, rateColor(u.FiveHourPct))
		}
	case card == cardUsageReset && u != nil:
		drawUsageClock(&f, u, now)
	case card == cardUsage7d && u != nil && u.SevenDayPct != nil:
		drawUnitPctFace(&f, "7d", *u.SevenDayPct)
	case card == cardUsageModelA && u != nil && len(u.Models) > 0:
		drawUnitPctFace(&f, u.Models[0].Marker, u.Models[0].Pct)
	case card == cardUsageModelB && u != nil && len(u.Models) > 1:
		drawUnitPctFace(&f, u.Models[1].Marker, u.Models[1].Pct)
	case card == cardSource && s.Source != "":
		drawDigits(&f, sourceCardText(s.Source), numStart, 1, sourceColorOr(s, colorWhite))
	default:
		// card == cardNone (no cards available): blank number slot.
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
// G.2 idle-restore countdown. No digits, no glass, no text — the body dims
// to ~40% white and the rest of the matrix stays dark, leaving an
// unambiguous "AI idle" signal that's also visually distinct from the
// active rotation frames. The eye sockets / cursor overlay are left dark
// (not painted) so the sprite silhouette is preserved even in the idle dim.
// Includes prio+force+lifetime so AWTRIX keeps holding the slot until the
// countdown elapses and we stop publishing.
func RenderIdleFrame(lifetimeSeconds int) map[string]any {
	// Idle dims the body only; deliberately skips the feature overlay so the
	// eye sockets / cursor remain dark, preserving the sprite silhouette.
	pixels := composeToolIconBodyPixels(Session{State: "idle"}, idleDimWhite)
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

// idleUsageTools is the fixed tool order for idle usage faces.
var idleUsageTools = []string{"claude", "codex"}

// RenderIdleUsagePayload renders the idle-with-hot-usage frame: dimmed tool
// icon + one usage face + the dimmed threshold bar. views holds only tools
// over the threshold (the coordinator gates); cursor picks the face across
// all hot tools (claude faces first, then codex), wrapping. Returns nil when
// no tool is hot — the caller then simply stops publishing (classic idle-off).
func RenderIdleUsagePayload(views map[string]*UsageView, cursor int, now time.Time, lifetimeSeconds int) map[string]any {
	type face struct {
		tool   string
		weekly bool
	}
	var faces []face
	for _, tool := range idleUsageTools {
		u := views[tool]
		if u == nil {
			continue
		}
		faces = append(faces, face{tool, false})
		if u.SevenDayPct != nil {
			faces = append(faces, face{tool, true})
		}
	}
	if len(faces) == 0 {
		return nil
	}
	// Modulo that handles negative cursor values safely.
	fc := faces[((cursor%len(faces))+len(faces))%len(faces)]
	u := views[fc.tool]

	var f Frame
	paintBitmap(&f, 0, 0, toolIcon8(Session{Tool: fc.tool}), idleDimWhite)
	if fc.weekly {
		drawUnitPctFace(&f, "7d", *u.SevenDayPct)
		drawBarInto(&f, *u.SevenDayPct)
	} else {
		drawUsageClock(&f, u, now)
		drawBarInto(&f, u.FiveHourPct)
	}
	return frameToCustomApp(&f, lifetimeSeconds)
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

// claudeEyes8 lights the robot-face eye sockets in the state colour.
// Each eye is 1 col × 2 rows; there are two eyes (cols 2 and 5, rows 2-3).
// These positions are holes in usageIconClaude (body sprite), so painting
// the overlay fills the sockets without disturbing the body.
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

// sourceColorOr returns the session's source colour when it parses, else fallback.
func sourceColorOr(s Session, fallback RGB) RGB {
	if s.SourceColor != nil {
		if c, ok := parseHex(*s.SourceColor); ok {
			return c
		}
	}
	return fallback
}

// iconBodyColor returns the colour for the 8×8 icon body: the session's source
// colour when present and valid, else iconNeutral. The state channel lives in
// the eye/cursor overlay, so the body must never fall back to a state colour.
func iconBodyColor(s Session) RGB {
	return sourceColorOr(s, iconNeutral)
}

// iconOverlay8 returns the 8×8 feature overlay for a session: the Codex cursor
// bitmap for Codex sessions, else the Claude eye-socket bitmap.
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

// packIcon8 extracts the 8×8 top-left region of f into a 64-int row-major
// pixel array (0xRRGGBB; undirty cells emit 0). It is the 8-wide analogue of
// framePixels and is the shared packing step for the icon db ops.
func packIcon8(f *Frame) []int {
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

// composeToolIconBodyPixels paints only the body (no overlay) of the 8×8 tool
// icon into a 64-int pixel array. Used by RenderIdleFrame so the idle dim
// covers the body but deliberately leaves the eye sockets / cursor dark,
// preserving the sprite silhouette.
func composeToolIconBodyPixels(s Session, body RGB) []int {
	var f Frame
	paintBitmap(&f, 0, 0, toolIcon8(s), body)
	return packIcon8(&f)
}

// composeToolIconPixels paints the full 8×8 icon (body + feature overlay) into
// a tight 8×8 = 64-int pixel array (for the locked-attention db, leaving cols
// 8-31 clear for native text). body is the identity colour; feature is the
// state colour for the eye/cursor overlay.
func composeToolIconPixels(s Session, body, feature RGB) []int {
	var f Frame
	drawToolIcon8(&f, s, body, feature)
	return packIcon8(&f)
}
