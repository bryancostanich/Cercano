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

type scrollbarOrientation int

const (
	scrollbarVertical scrollbarOrientation = iota
	scrollbarHorizontal
)

type scrollbarState struct {
	Total    int
	Viewport int
	Offset   int
	Length   int
}

type scrollbarMetrics struct {
	ThumbStart int
	ThumbSize  int
	MaxOffset  int
	Overflow   bool
}

func computeScrollbarMetrics(state scrollbarState) scrollbarMetrics {
	if state.Length <= 0 || state.Viewport <= 0 || state.Total <= state.Viewport {
		return scrollbarMetrics{}
	}
	thumbSize := roundDiv(state.Length*state.Viewport, state.Total)
	if thumbSize < 1 {
		thumbSize = 1
	}
	if thumbSize > state.Length {
		thumbSize = state.Length
	}
	maxStart := state.Length - thumbSize
	maxOffset := state.Total - state.Viewport
	offset := clampInt(state.Offset, 0, maxOffset)
	thumbStart := roundDiv(offset*maxStart, maxOffset)
	thumbStart = clampInt(thumbStart, 0, maxStart)
	return scrollbarMetrics{ThumbStart: thumbStart, ThumbSize: thumbSize, MaxOffset: maxOffset, Overflow: true}
}

func scrollbarOffsetFromPosition(pos int, state scrollbarState) int {
	metrics := computeScrollbarMetrics(state)
	if !metrics.Overflow || state.Length <= 1 {
		return 0
	}
	pos = clampInt(pos, 0, state.Length-1)
	off := roundDiv(pos*metrics.MaxOffset, state.Length-1)
	return clampInt(off, 0, metrics.MaxOffset)
}

func scrollbarGlyphs(state scrollbarState) []rune {
	glyphs := make([]rune, state.Length)
	metrics := computeScrollbarMetrics(state)
	for i := range glyphs {
		switch {
		case !metrics.Overflow:
			glyphs[i] = ' '
		case i >= metrics.ThumbStart && i < metrics.ThumbStart+metrics.ThumbSize:
			glyphs[i] = '█'
		default:
			glyphs[i] = '░'
		}
	}
	return glyphs
}

// scrollbarThumb computes the thumb's row range within a bar of `height` rows.
// ok is false when there is no overflow (total <= height) — the caller paints
// a blank column in that case.
func scrollbarThumb(total, height, yOffset int) (thumbTop, thumbSize int, ok bool) {
	metrics := computeScrollbarMetrics(scrollbarState{Total: total, Viewport: height, Offset: yOffset, Length: height})
	return metrics.ThumbStart, metrics.ThumbSize, metrics.Overflow
}

// scrollbarColumn returns `height` runes, one per viewport row: '█' for the
// thumb, '░' for the track, ' ' when there is no overflow. The caller styles
// each rune (thumb → Border grey, track → BorderDim grey, space → plain).
func scrollbarColumn(total, height, yOffset int) []rune {
	return scrollbarGlyphs(scrollbarState{Total: total, Viewport: height, Offset: yOffset, Length: height})
}

// scrollOffsetFromClick maps an absolute screen row to a clamped viewport
// YOffset. top is the viewport's first screen row; height its row count;
// total the content line count.
func scrollOffsetFromClick(clickRow, top, height, total int) int {
	return scrollbarOffsetFromPosition(clickRow-top, scrollbarState{Total: total, Viewport: height, Length: height})
}
