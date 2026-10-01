package render

const (
	panelW = 32

	iconW   = 8
	iconOpW = iconW + 1

	contentX = iconOpW
	contentW = panelW - contentX
	textRow  = 1

	rightSlotX = 25
	resetMarkX = rightSlotX + 2

	barRow = 7
	barX0  = iconW
	barW   = panelW - barX0
)

func iconOp(icon []int) []any {
	data := make([]int, iconOpW*8)
	for y := 0; y < 8; y++ {
		copy(data[y*iconOpW:y*iconOpW+iconW], icon[y*iconW:(y+1)*iconW])
	}
	return bitmapOp(0, 0, iconOpW, 8, data)
}
