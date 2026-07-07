// Package banner renders the cercano-cli splash banner. V0: static F-refined
// layout. Shimmer animation lands in a later phase.
package banner

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// Width is the banner's minimum outer width in columns: 1 left wall + 60 inner
// + 1 right wall. The box grows beyond this only when the status line needs the
// room (long model names); it never shrinks below it, so callers can keep using
// Width as a layout floor.
const Width = 62

// wordmark rows (28 cols each).
const (
	wordmarkTop = "█▀▀ █▀▀ █▀█ █▀▀ █▀█ █▄ █ █▀█"
	wordmarkBot = "█▄▄ ██▄ █▀▄ █▄▄ █▀█ █ ▀█ █▄█"
)

// Meta is the customizable status line under the lime rail.
type Meta struct {
	Tagline string // e.g. "local-first ai coprocessor"
	Version string // e.g. "v0.1.0"
	Model   string // e.g. "qwen3-coder"
}

// WordmarkCols is the visible column count of the wordmark rows. The shimmer
// math uses this to map a sweep position into per-column color choices.
const WordmarkCols = 28

// Render returns the 8-line banner as a single string, styled with the given palette.
// Returned string contains terminal escape sequences; render width per line is
// at least `Width` (the box grows to fit a long status line).
// Equivalent to RenderWithSweep(p, m, +infinity) — no shimmer applied.
func Render(p theme.Palette, m Meta) string {
	return renderWith(p, m, func(rune, int) color.Color { return p.Primary })
}

// RenderWithSweep renders the banner with a moving shimmer band over the wordmark.
// `sweepPos` is the bright-band head position in column space; values outside
// [-tail, WordmarkCols+tail] render as plain Render. The angle adds a half-column
// offset between the two wordmark rows so the band crosses on a mild `/` lean.
func RenderWithSweep(p theme.Palette, m Meta, sweepPos float64) string {
	const angleHalf = 0.5
	colorTop := makeShimmerColorFn(p, sweepPos+angleHalf)
	colorBot := makeShimmerColorFn(p, sweepPos-angleHalf)
	return renderWithRowColors(p, m, colorTop, colorBot)
}

// renderWith is the common renderer; topColor and botColor (here, both the
// same function) decide each wordmark cell's color.
func renderWith(p theme.Palette, m Meta, colorFn func(r rune, col int) color.Color) string {
	return renderWithRowColors(p, m, colorFn, colorFn)
}

func renderWithRowColors(p theme.Palette, m Meta, colorTop, colorBot func(rune, int) color.Color) string {
	s := theme.NewStyles(p)
	// Status line content width (visible columns). The model segment is
	// omitted entirely when unset so we never render a dangling "· "
	// separator in the brief window before the config load fills it in.
	statusVisible := visibleWidth(m.Tagline) + 2 /*▶ */ + 7 /*     · */ + visibleWidth(m.Version)
	if m.Model != "" {
		statusVisible += 3 /* · */ + visibleWidth(m.Model)
	}

	// Inner width between the walls. The floor is the wordmark block — 60 cols
	// (2 left pad + 28 wordmark + 30 right pad), i.e. Width-2 — so short model
	// names keep the familiar box. The box grows only when the status line
	// (2-col left pad + content + 2-col right breathing pad) needs more room,
	// so long model names never clip past the right wall.
	inner := Width - 2
	if statusInner := 2 /*left pad*/ + statusVisible + 2 /*right pad*/; statusInner > inner {
		inner = statusInner
	}

	borderTop := s.BorderDim.Render("╔" + strings.Repeat("═", inner) + "╗")
	borderBot := s.BorderDim.Render("╚" + strings.Repeat("═", inner) + "╝")
	wall := s.BorderDim.Render("║")
	blank := wall + strings.Repeat(" ", inner) + wall

	// Wordmark rows: 2-col left pad, 28-col wordmark, remaining right pad. Each
	// wordmark cell rendered independently so per-column colors can change.
	wordmarkLine := func(text string, cf func(rune, int) color.Color) string {
		var b strings.Builder
		col := 0
		for _, r := range text {
			c := cf(r, col)
			b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
			col++
		}
		return wall + "  " + b.String() + strings.Repeat(" ", inner-2-WordmarkCols) + wall
	}

	// Lime rail: 2-col left pad, rail, 2-col right pad.
	railLine := wall + "  " + s.Accent.Render(strings.Repeat("━", inner-4)) + "  " + wall

	// Status line: 2-col left pad, content, trailing pad to the right wall.
	statusParts := []string{
		s.Muted.Render("▶ "),
		s.Primary.Render(m.Tagline),
		s.Muted.Render("     · "),
		s.Info.Render(m.Version),
	}
	if m.Model != "" {
		statusParts = append(statusParts,
			s.Muted.Render(" · "),
			s.Accent.Render(m.Model),
		)
	}
	status := lipgloss.JoinHorizontal(lipgloss.Left, statusParts...)
	trailingPad := inner - 2 /*left pad*/ - statusVisible
	if trailingPad < 0 {
		trailingPad = 0
	}
	statusLine := wall + "  " + status + strings.Repeat(" ", trailingPad) + wall

	rows := []string{
		borderTop,
		blank,
		wordmarkLine(wordmarkTop, colorTop),
		wordmarkLine(wordmarkBot, colorBot),
		blank,
		railLine,
		statusLine,
		borderBot,
	}
	return strings.Join(rows, "\n")
}

// visibleWidth returns the number of display columns a string occupies,
// counting runes (not bytes). Good enough for the banner where all chars are
// single-cell ASCII or block-drawing.
func visibleWidth(s string) int { return lipgloss.Width(s) }

// makeShimmerColorFn returns a per-column color function for one wordmark row.
// Distance from the sweep head selects amber → bright → white via piecewise lerp.
func makeShimmerColorFn(p theme.Palette, sweepPos float64) func(rune, int) color.Color {
	const tail = 5.0
	base := colorToRGB(p.Primary)
	bright := colorToRGB(p.Bright)
	white := [3]uint8{0xFF, 0xFF, 0xFF}

	return func(r rune, col int) color.Color {
		// Spaces inside the wordmark (e.g. the N letter's gap) stay base; the
		// space isn't painted anyway, but the lookup must not crash.
		dist := float64(col) - sweepPos
		ad := dist
		if ad < 0 {
			ad = -ad
		}
		if ad >= tail {
			return p.Primary
		}
		k := 1.0 - ad/tail // 0 at edge, 1 at peak
		var c [3]uint8
		if k < 0.5 {
			c = lerpRGB(base, bright, k*2)
		} else {
			c = lerpRGB(bright, white, (k-0.5)*2)
		}
		return lipgloss.Color(rgbToHex(c))
	}
}

// colorToRGB extracts the R/G/B components from a color.Color.
// The alpha-premultiplied 16-bit values from RGBA() are scaled back to 8-bit.
func colorToRGB(c color.Color) [3]uint8 {
	r, g, b, _ := c.RGBA()
	return [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}

func lerpRGB(a, b [3]uint8, t float64) [3]uint8 {
	return [3]uint8{
		uint8(float64(a[0]) + (float64(b[0])-float64(a[0]))*t),
		uint8(float64(a[1]) + (float64(b[1])-float64(a[1]))*t),
		uint8(float64(a[2]) + (float64(b[2])-float64(a[2]))*t),
	}
}

func rgbToHex(c [3]uint8) string {
	const hex = "0123456789ABCDEF"
	out := []byte("#000000")
	out[1] = hex[c[0]>>4]
	out[2] = hex[c[0]&0xF]
	out[3] = hex[c[1]>>4]
	out[4] = hex[c[1]&0xF]
	out[5] = hex[c[2]>>4]
	out[6] = hex[c[2]&0xF]
	return string(out)
}
