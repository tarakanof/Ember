package render

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// RGB is a 24-bit colour.
type RGB struct {
	R, G, B uint8
}

// Frame is a 32×8 pixel buffer.
type Frame struct {
	Pixels [8][32]RGB
	Dirty  [8][32]bool
	// Native, when set, is text the FIRMWARE renders in its own font on top of
	// this bitmap, rather than something painted into Pixels.
	Native *NativeText
}

// NativeText is firmware-rendered text laid over a Frame's bitmap.
type NativeText struct {
	Text  string
	X     int
	W     int
	Color RGB
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
type UsageView struct {
	FiveHourPct int
	// ResetLabel is the host-local "HH:MM"; empty falls back to an hourglass from ResetAt.
	ResetLabel  string
	ResetAt     int64 // unix; used only when ResetLabel is ""
	SevenDayPct *int  // nil when the 7d window is unknown
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
	// Source and Tool identify the winning session (empty when none), so thin
	// clients need not re-run PickWinning over sessions[] to learn the host.
	Source string `json:"source"`
	Tool   string `json:"tool"`
}

func paintCell(f *Frame, x, y int, c RGB) {
	if x < 0 || x >= 32 || y < 0 || y >= 8 {
		return
	}
	f.Pixels[y][x] = c
	f.Dirty[y][x] = true
}

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

func paintBitmap(f *Frame, ox, oy int, sprite []string, c RGB) {
	for y, row := range sprite {
		for x, ch := range row {
			if ch == 'X' {
				paintCell(f, ox+x, oy+y, c)
			}
		}
	}
}

const glassGlyph = '⌷'

const resetGlyph = '⧗'

var font3x5 = map[rune][]string{
	'0':        {"XXX", "X.X", "X.X", "X.X", "XXX"},
	'1':        {".X.", "XX.", ".X.", ".X.", "XXX"},
	'2':        {"XXX", "..X", "XXX", "X..", "XXX"},
	'3':        {"XXX", "..X", "XXX", "..X", "XXX"},
	'4':        {"X.X", "X.X", "XXX", "..X", "..X"},
	'5':        {"XXX", "X..", "XXX", "..X", "XXX"},
	'6':        {"XXX", "X..", "XXX", "X.X", "XXX"},
	'7':        {"XXX", "..X", "..X", "..X", "..X"},
	'8':        {"XXX", "X.X", "XXX", "X.X", "XXX"},
	'9':        {"XXX", "X.X", "XXX", "..X", "XXX"},
	'/':        {"..X", "..X", ".X.", "X..", "X.."},
	'+':        {"...", ".X.", "XXX", ".X.", "..."},
	'-':        {"...", "...", "XXX", "...", "..."},
	'%':        {"X.X", "..X", ".X.", "X..", "X.X"},
	'°':        {"XXX", "X.X", "XXX", "...", "..."},
	':':        {".", "X", ".", "X", "."},
	'h':        {"X..", "X..", "XXX", "X.X", "X.X"},
	'd':        {"..X", "..X", "XXX", "X.X", "XXX"},
	'O':        {"XXX", "X.X", "X.X", "X.X", "XXX"},
	'P':        {"XXX", "X.X", "XXX", "X..", "X.."},
	'S':        {"XXX", "X..", "XXX", "..X", "XXX"},
	glassGlyph: {"X.X", "X.X", "X.X", "XXX", "XXX"},
	resetGlyph: {"XXX", "X.X", ".X.", "X.X", "XXX"},
	'A':        {"XXX", "X.X", "XXX", "X.X", "X.X"},
	'B':        {"XX.", "X.X", "XX.", "X.X", "XX."},
	'C':        {"XXX", "X..", "X..", "X..", "XXX"},
	'D':        {"XX.", "X.X", "X.X", "X.X", "XX."},
	'E':        {"XXX", "X..", "XXX", "X..", "XXX"},
	'F':        {"XXX", "X..", "XXX", "X..", "X.."},
	'G':        {"XXX", "X..", "X.X", "X.X", "XXX"},
	'H':        {"X.X", "X.X", "XXX", "X.X", "X.X"},
	'I':        {"XXX", ".X.", ".X.", ".X.", "XXX"},
	'J':        {"..X", "..X", "..X", "X.X", "XXX"},
	'K':        {"X.X", "X.X", "XX.", "X.X", "X.X"},
	'L':        {"X..", "X..", "X..", "X..", "XXX"},
	'M':        {"XXX", "XXX", "X.X", "X.X", "X.X"},
	'N':        {"X.X", "XXX", "X.X", "X.X", "X.X"},
	'Q':        {"XXX", "X.X", "X.X", "XXX", "..X"},
	'R':        {"XX.", "X.X", "XX.", "X.X", "X.X"},
	'T':        {"XXX", ".X.", ".X.", ".X.", ".X."},
	'U':        {"X.X", "X.X", "X.X", "X.X", "XXX"},
	'V':        {"X.X", "X.X", "X.X", "X.X", ".X."},
	'W':        {"X.X", "X.X", "XXX", "XXX", "X.X"},
	'X':        {"X.X", "X.X", ".X.", "X.X", "X.X"},
	'Y':        {"X.X", "X.X", ".X.", ".X.", ".X."},
	'Z':        {"XXX", "..X", ".X.", "X..", "XXX"},
}

func glyph(r rune) []string {
	g, ok := font3x5[r]
	if !ok {
		return nil
	}
	return g
}

func drawDigits(f *Frame, text string, startX, startY int, c RGB) {
	x := startX
	for _, ch := range text {
		g := glyph(ch)
		if g != nil {
			paintBitmap(f, x, startY, g, c)
		}
		x += 4
	}
}

const (
	glassLeft        = rightSlotX
	glassRight       = panelW - 1
	glassTopRow      = 1
	glassBottomRow   = 5
	glassInteriorW   = 5
	glassInteriorH   = 4
	glassInteriorPix = glassInteriorW * glassInteriorH
)

var glassWall = RGB{0xcc, 0xcc, 0xcc}

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
	n := (v*glassInteriorPix + 50) / 100
	if n > glassInteriorPix {
		n = glassInteriorPix
	}
	var colOrder [glassInteriorW]int
	for i := range colOrder {
		colOrder[i] = glassLeft + 1 + i
	}
	for row := 0; row < glassInteriorH && n > 0; row++ {
		y := (glassBottomRow - 1) - row
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
			prio:  StatePriority(s.State),
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
		if i >= barW {
			break
		}
		paintCell(f, barX0+i, barRow, e.color)
	}
}

func drawRateBar(f *Frame, pct int) {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	for i, c := range usageBarPixels(pct) {
		paintCell(f, barX0+i, barRow, c)
	}
}

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

func framePixelsRect(f *Frame, x0, y0, w, h int) []int {
	out := make([]int, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := x0+x, y0+y
			if fx < 0 || fx >= 32 || fy < 0 || fy >= 8 || !f.Dirty[fy][fx] {
				continue
			}
			c := f.Pixels[fy][fx]
			out[y*w+x] = (int(c.R) << 16) | (int(c.G) << 8) | int(c.B)
		}
	}
	return out
}

const rotateDwellSeconds = 6

func drawOpsAround(f *Frame, n *NativeText) []any {
	right := n.X + n.W
	ops := []any{}
	if n.X > 0 {
		ops = append(ops, bitmapOp(0, 0, n.X, 8, framePixelsRect(f, 0, 0, n.X, 8)))
	}
	if right < 32 {
		ops = append(ops, bitmapOp(right, 0, 32-right, 8, framePixelsRect(f, right, 0, 32-right, 8)))
	}
	ops = append(ops, bitmapOp(n.X, barRow, n.W, 1, framePixelsRect(f, n.X, barRow, n.W, 1)))
	return ops
}

func frameToCustomApp(f *Frame, lifetimeSeconds int, hold bool) map[string]any {
	p := map[string]any{
		"draw":       []any{bitmapOp(0, 0, 32, 8, framePixels(f))},
		"lifetimeMs": msOf(lifetimeSeconds),
		"durationMs": msOf(rotateDwellSeconds),
	}
	if n := f.Native; n != nil && n.Text != "" {
		p["draw"] = drawOpsAround(f, n)
		p["text"] = n.Text
		p["textColor"] = hexOf(n.Color)
		p["textOffsetX"] = n.X
		p["textCenter"] = false
		p["scroll"] = scrollStaticWhenFits()
	}
	if hold {
		applyHold(p, lifetimeSeconds)
	}
	return p
}

var (
	colorRunning = RGB{0x2e, 0xe8, 0x5e}
	colorWaiting = RGB{0xff, 0xc1, 0x4d}
	colorError   = RGB{0xff, 0x3a, 0x3a}
	colorDone    = RGB{0x4f, 0xa9, 0xff}
	colorWhite   = RGB{0xff, 0xff, 0xff}
)

const cardNone = -1

const (
	cardSource = iota
	cardTool
	cardUsage5h
	cardUsageReset
	cardUsage7d
	cardUsageModelA
	cardUsageModelB
)

func isUsageCard(card int) bool {
	switch card {
	case cardUsage5h, cardUsageReset, cardUsage7d, cardUsageModelA, cardUsageModelB:
		return true
	}
	return false
}

func sourceCardEnabled(s Session) bool { return s.SourceCard == nil || *s.SourceCard }
func sessionBarEnabled(s Session) bool { return s.SessionBar == nil || *s.SessionBar }

const sourceNameMaxW = rightSlotX - contentX - 1

const ngASCIIInkW = "11333331223323133333333333123333333333333133354334333335333333332333333331333333333333333333133"

const ngWideGlyphW = 5

func ngGlyphW(r rune) int {
	if r >= 0x20 && r <= 0x7E {
		return int(ngASCIIInkW[r-0x20] - '0')
	}
	return ngWideGlyphW
}

func sourceCardText(source string) string {
	var out []rune
	w := 0
	for _, r := range strings.ToUpper(source) {
		next := w + ngGlyphW(r)
		if len(out) > 0 {
			next++
		}
		if next > sourceNameMaxW {
			break
		}
		out = append(out, r)
		w = next
	}
	return string(out)
}

// AvailableCards returns the cards this session offers, in rotation order.
func AvailableCards(s Session, u *UsageView) []int {
	var cards []int
	if sourceCardEnabled(s) && s.Source != "" {
		cards = append(cards, cardSource)
	}
	if u != nil {
		cards = append(cards, cardUsage5h)
		if !s.RateBottomBar && (u.ResetLabel != "" || u.ResetAt > 0) {
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

func rateText(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 99 {
		pct = 99
	}
	return itoa(pct) + "%"
}

func resetText(resetAt int64, now time.Time) (string, RGB) {
	remaining := resetAt - now.Unix()
	if remaining < 0 {
		remaining = 0
	}
	hours := int((remaining + 3599) / 3600)
	if hours > 9 {
		hours = 9
	}
	color := usageOK
	if remaining < 3600 {
		color = usageWarn
	}
	return itoa(hours) + string(resetGlyph), color
}

func drawUsageClock(f *Frame, u *UsageView, now time.Time) {
	if u.ResetLabel != "" {
		drawClockInto(f, u.ResetLabel, contentX)
		drawDigits(f, string(resetGlyph), resetMarkX, textRow, usageGray)
		return
	}
	text, col := resetText(u.ResetAt, now)
	drawDigits(f, text, contentX, textRow, col)
	drawUsageUnit(f, "5h")
}

func drawUsageUnit(f *Frame, unit string) {
	drawDigits(f, unit, rightSlotX, 1, usageGray)
}

func drawUnitPctFace(f *Frame, unit string, pct int) {
	drawDigits(f, rateText(pct), contentX, textRow, usageThreshold(pct))
	drawUsageUnit(f, unit)
}

// PickWinning returns the priority-winning session, its state colour, and the
// number of active sessions (waiting, error, running or done).
func PickWinning(sessions []Session) (win *Session, color RGB, total int) {
	for i := range sessions {
		s := &sessions[i]
		prio := StatePriority(s.State)
		if prio == priorityInactive {
			continue
		}
		total++
		if win == nil {
			win = s
			continue
		}
		best := StatePriority(win.State)
		if prio < best || (prio == best && s.UpdatedAt.After(win.UpdatedAt)) {
			win = s
		}
	}
	if win == nil {
		return nil, RGB{}, total
	}
	return win, colorForState(win.State), total
}

func sessionKey(s Session) string {
	return s.Key()
}

// SessionByKey returns the session in snap whose canonical key matches, or the
// zero Session when absent.
func SessionByKey(snap Snapshot, key string) Session {
	for i := range snap.Sessions {
		if sessionKey(snap.Sessions[i]) == key {
			return snap.Sessions[i]
		}
	}
	return Session{}
}

const priorityInactive = 4

// StatePriority ranks a session state for display, lower first: waiting 0,
// error 1, running 2, done 3, idle or unknown priorityInactive.
func StatePriority(state string) int {
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
		return priorityInactive
	}
}

// SortedActiveKeys returns the canonical keys of non-idle sessions in rotation
// order: state-priority first, then (source, tool, session) lexicographically.
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
			prio: StatePriority(s.State),
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

// PickRotated advances the rotation pointer.
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

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return strconv.Itoa(n)
}

func detailPayload(s Session, sessions []Session, text, hexColor string, blink bool, lifetimeSeconds int, hold bool) map[string]any {
	pixels := composeToolIconPixels(s, iconBodyColor(s), colorForState(s.State))
	draw := []any{iconOp(pixels)}
	var bar Frame
	if drawBottomBar(&bar, s, sessions) {
		draw = append(draw, bitmapOp(barX0, barRow, barW, 1, framePixelsRect(&bar, barX0, barRow, barW, 1)))
	}
	p := map[string]any{
		"draw":        draw,
		"text":        text,
		"textColor":   hexColor,
		"textOffsetX": 9,
		"textCenter":  false,
		"scroll":      scrollStaticWhenFits(),
		"durationMs":  msOf(rotateDwellSeconds),
		"lifetimeMs":  msOf(lifetimeSeconds),
	}
	if hold {
		applyHold(p, lifetimeSeconds)
	}
	if blink {
		p["textBlinkMs"] = 500
	}
	return p
}

func chooseSession(snap Snapshot, keys []string, pointer string) *Session {
	chosen := pointer
	if !slices.Contains(keys, chosen) {
		chosen = keys[0]
	}
	for i := range snap.Sessions {
		if sessionKey(snap.Sessions[i]) == chosen {
			return &snap.Sessions[i]
		}
	}
	return nil
}

// AttentionHeld reports whether RenderForCoord will emit the held attention
// frame for this snapshot/pointer/lock combination — i.e. whether the frame
// claims the display hold and the caller must pin the app device-side too.
func AttentionHeld(snap Snapshot, pointer string, locked bool) bool {
	if !locked {
		return false
	}
	keys := SortedActiveKeys(snap)
	if len(keys) == 0 {
		return false
	}
	s := chooseSession(snap, keys, pointer)
	return s != nil && (s.State == "waiting" || s.State == "error")
}

// RenderForCoord composes the awtrix-ng pushed-app payload for the
// coordinator's current display state.
func RenderForCoord(snap Snapshot, pointer string, card int, locked bool, lifetimeSeconds int, usage map[string]*UsageView) map[string]any {
	keys := SortedActiveKeys(snap)
	if len(keys) == 0 {
		return nil
	}
	session := chooseSession(snap, keys, pointer)
	if session == nil {
		return nil
	}
	if locked && (session.State == "waiting" || session.State == "error") {
		label, hex := attentionLabelAndColor(session.State)
		if session.Source != "" {
			label += " " + strings.ToUpper(session.Source)
		}
		return detailPayload(*session, snap.Sessions, label, hex, true, lifetimeSeconds, true)
	}

	u := usage[session.Tool]

	cards := AvailableCards(*session, u)
	selected := cardNone
	if len(cards) > 0 {
		ci := card
		if ci < 0 || ci >= len(cards) {
			ci = 0
		}
		selected = cards[ci]
	}
	if selected == cardTool {
		return detailPayload(*session, snap.Sessions, session.Activity, stateHex(session.State), false, lifetimeSeconds, false)
	}
	frame := ComposeFrame(*session, selected, u, snap.Sessions, snap.Now)
	return frameToCustomApp(&frame, lifetimeSeconds, false)
}

// ComposeFrame paints the standard layout for one session.
func ComposeFrame(s Session, card int, u *UsageView, sessions []Session, now time.Time) Frame {
	var f Frame
	drawToolIcon8(&f, s, iconBodyColor(s), colorForState(s.State))

	switch {
	case card == cardUsage5h && u != nil:
		if s.RateBottomBar {
			drawUsageClock(&f, u, now)
		} else {
			drawUnitPctFace(&f, "5h", u.FiveHourPct)
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
		f.Native = &NativeText{
			Text:  sourceCardText(s.Source),
			X:     contentX,
			W:     glassLeft - contentX,
			Color: sourceColorOr(s, colorWhite),
		}
	default:
	}

	if !isUsageCard(card) {
		drawGlass(&f, s.ContextPct, colorForState(s.State))
	}

	drawBottomBar(&f, s, sessions)
	return f
}

func drawBottomBar(f *Frame, s Session, sessions []Session) bool {
	switch {
	case s.RateBottomBar && s.RateWindowPct != nil:
		pct := *s.RateWindowPct
		drawRateBar(f, pct)
	case sessionBarEnabled(s):
		drawSessionBar(f, sessions)
	default:
		return false
	}
	for x := barX0; x < panelW; x++ {
		if f.Dirty[barRow][x] {
			return true
		}
	}
	return false
}

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

func attentionLabelAndColor(state string) (string, string) {
	if state == "error" {
		return "ERR", hexOf(colorError)
	}
	return "WAIT", hexOf(colorWaiting)
}

func stateHex(state string) string { return hexOf(colorForState(state)) }

var idleDimWhite = RGB{0x66, 0x66, 0x66}

// RenderIdleFrame returns the dimmed-robot payload emitted during the G.2
// idle-restore countdown.
func RenderIdleFrame(lifetimeSeconds int) map[string]any {
	pixels := composeToolIconBodyPixels(Session{State: "idle"}, idleDimWhite)
	p := map[string]any{
		"draw":       []any{bitmapOp(0, 0, 8, 8, pixels)},
		"lifetimeMs": msOf(lifetimeSeconds),
	}
	applyHold(p, lifetimeSeconds)
	return p
}

var idleUsageTools = []string{"claude", "codex"}

// RenderIdleUsagePayload renders the idle-with-hot-usage frame: dimmed tool
// icon + one usage face + the dimmed threshold bar.
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
	return frameToCustomApp(&f, lifetimeSeconds, true)
}

func toolIcon8(s Session) []string {
	if s.Tool == "codex" {
		return usageIconCodex
	}
	return usageIconClaude
}

var iconNeutral = RGB{0xcc, 0xcc, 0xcc}

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

func sourceColorOr(s Session, fallback RGB) RGB {
	if s.SourceColor != nil {
		if c, ok := parseHex(*s.SourceColor); ok {
			return c
		}
	}
	return fallback
}

func iconBodyColor(s Session) RGB {
	return sourceColorOr(s, iconNeutral)
}

func iconOverlay8(s Session) []string {
	if s.Tool == "codex" {
		return codexCursor8
	}
	return claudeEyes8
}

func drawToolIcon8(f *Frame, s Session, body, feature RGB) {
	paintBitmap(f, 0, 0, toolIcon8(s), body)
	paintBitmap(f, 0, 0, iconOverlay8(s), feature)
}

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

func composeToolIconBodyPixels(s Session, body RGB) []int {
	var f Frame
	paintBitmap(&f, 0, 0, toolIcon8(s), body)
	return packIcon8(&f)
}

func composeToolIconPixels(s Session, body, feature RGB) []int {
	var f Frame
	drawToolIcon8(&f, s, body, feature)
	return packIcon8(&f)
}
