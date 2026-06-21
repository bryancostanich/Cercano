// Package banner renders the cercano-cli splash banner. V0: static F-refined
// layout. Shimmer animation lands in a later phase.
package banner

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"cercano/source/server/internal/cli/theme"
)

// Width is the fixed banner outer width in columns: 1 left wall + 60 inner + 1 right wall.
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
// Returned string contains terminal escape sequences; render width per line is `Width`.
// Equivalent to RenderWithSweep(p, m, +infinity) — no shimmer applied.
func Render(p theme.Palette, m Meta) string {
	return renderWith(p, m, func(rune, int) lipgloss.Color { return p.Primary })
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
func renderWith(p theme.Palette, m Meta, colorFn func(r rune, col int) lipgloss.Color) string {
	return renderWithRowColors(p, m, colorFn, colorFn)
}

func renderWithRowColors(p theme.Palette, m Meta, colorTop, colorBot func(rune, int) lipgloss.Color) string {
	s := theme.NewStyles(p)
	borderTop := s.BorderDim.Render("╔" + strings.Repeat("═", Width-2) + "╗")
	borderBot := s.BorderDim.Render("╚" + strings.Repeat("═", Width-2) + "╝")
	wall := s.BorderDim.Render("║")
	blank := wall + strings.Repeat(" ", Width-2) + wall

	// Wordmark rows: 2-col left pad, 28-col wordmark, 30-col right pad. Each
	// wordmark cell rendered independently so per-column colors can change.
	wordmarkLine := func(text string, cf func(rune, int) lipgloss.Color) string {
		var b strings.Builder
		col := 0
		for _, r := range text {
			c := cf(r, col)
			b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
			col++
		}
		return wall + "  " + b.String() + strings.Repeat(" ", 30) + wall
	}

	// Lime rail: 2-col left pad, 56-col rail, 2-col right pad.
	railLine := wall + "  " + s.Accent.Render(strings.Repeat("━", 56)) + "  " + wall

	// Status line: 2-col left pad, 55-col content, 3-col right pad.
	status := lipgloss.JoinHorizontal(lipgloss.Left,
		s.Muted.Render("▶ "),
		s.Primary.Render(m.Tagline),
		s.Muted.Render("     · "),
		s.Info.Render(m.Version),
		s.Muted.Render(" · "),
		s.Accent.Render(m.Model),
	)
	// Compute trailing pad to land the right wall at column Width.
	statusVisible := visibleWidth(m.Tagline) + 2 /*▶ */ + 7 /*     · */ + visibleWidth(m.Version) + 3 /* · */ + visibleWidth(m.Model)
	trailingPad := Width - 2 /*walls*/ - 2 /*left pad*/ - statusVisible
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
func makeShimmerColorFn(p theme.Palette, sweepPos float64) func(rune, int) lipgloss.Color {
	const tail = 5.0
	base := hexToRGB(string(p.Primary))
	bright := hexToRGB(string(p.Bright))
	white := [3]uint8{0xFF, 0xFF, 0xFF}

	return func(r rune, col int) lipgloss.Color {
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

func hexToRGB(hex string) [3]uint8 {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return [3]uint8{0xEA, 0x82, 0x12}
	}
	var rgb [3]uint8
	for i := 0; i < 3; i++ {
		hi := hexDigit(hex[2*i])
		lo := hexDigit(hex[2*i+1])
		rgb[i] = (hi << 4) | lo
	}
	return rgb
}

func hexDigit(c byte) uint8 {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
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
