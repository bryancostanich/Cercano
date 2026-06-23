package ui

// scrollbar.go — pure geometry for the chat viewport's vertical scrollbar.
// No model state, no rendering: ints in, glyphs/ints out, so the logic is
// fully unit-testable. The renderer (View) and the drag handler (Update)
// both build on these.

// round returns n rounded to the nearest int (ties away from zero) for a
// numerator/denominator division, avoiding float math.
func roundDiv(num, den int) int {
	if den == 0 {
		return 0
	}
	return (num + den/2) / den
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// scrollbarThumb computes the thumb's row range within a bar of `height` rows.
// ok is false when there is no overflow (total <= height) — the caller paints
// a blank column in that case.
func scrollbarThumb(total, height, yOffset int) (thumbTop, thumbSize int, ok bool) {
	if height <= 0 || total <= height {
		return 0, 0, false
	}
	thumbSize = roundDiv(height*height, total)
	if thumbSize < 1 {
		thumbSize = 1
	}
	if thumbSize > height {
		thumbSize = height
	}
	maxTop := height - thumbSize
	maxOffset := total - height
	yOffset = clampInt(yOffset, 0, maxOffset)
	thumbTop = roundDiv(yOffset*maxTop, maxOffset)
	thumbTop = clampInt(thumbTop, 0, maxTop)
	return thumbTop, thumbSize, true
}

// scrollbarColumn returns `height` runes, one per viewport row: '█' for the
// thumb, '░' for the track, ' ' when there is no overflow. The caller styles
// each rune (thumb → Border grey, track → BorderDim grey, space → plain).
func scrollbarColumn(total, height, yOffset int) []rune {
	col := make([]rune, height)
	thumbTop, thumbSize, ok := scrollbarThumb(total, height, yOffset)
	for i := range col {
		switch {
		case !ok:
			col[i] = ' '
		case i >= thumbTop && i < thumbTop+thumbSize:
			col[i] = '█'
		default:
			col[i] = '░'
		}
	}
	return col
}

// scrollOffsetFromClick maps an absolute screen row to a clamped viewport
// YOffset. top is the viewport's first screen row; height its row count;
// total the content line count.
func scrollOffsetFromClick(clickRow, top, height, total int) int {
	maxOffset := total - height
	if maxOffset <= 0 || height <= 1 {
		return 0
	}
	rel := clampInt(clickRow-top, 0, height-1)
	off := roundDiv(rel*maxOffset, height-1)
	return clampInt(off, 0, maxOffset)
}
