package render

// The panel's column grid, shared by every renderer so that apps line up with
// each other as the device rotates between them. It copies the layout
// awtrix-ng itself uses for an app with an 8px icon: the icon owns cols 0-7,
// col 8 is the icon gap, text and charts start at col 9, and the native
// progress bar runs along row 7 from col 8, under the gap.
//
//	cols  0-7   icon (drawn 8×8 sprite or native icon)
//	col   8     icon gap: blank, except on row 7
//	cols  9-24  content: digits, native text
//	cols 25-31  right slot: context glass or usage unit label
//	row   7     bottom bar, cols 8-31, for every app that has one
//
// Every hand-coded 8/9/11 used to be its own literal, and they drifted apart:
// the session bar started at col 11 while the rate bar started at col 8.
const (
	panelW = 32

	iconW = 8 // the icon sprite: cols 0-7
	// iconOpW is the width of a drawn icon's bitmap op: the sprite plus the
	// blank gap column. Draw ops paint over text, so the zeros in col 8 keep
	// scrolling native text out of the gap, as NG's own icon column does.
	iconOpW = iconW + 1

	contentX = iconOpW // first column of text and digits
	contentW = panelW - contentX
	textRow  = 1 // top row of 3×5 glyphs; NG's small font also uses rows 1-5

	rightSlotX = 25 // context glass / usage unit label: cols 25-31

	barRow = 7
	barX0  = iconW // bottom bars start under the gap, like NG's native progress
	barW   = panelW - barX0
)
