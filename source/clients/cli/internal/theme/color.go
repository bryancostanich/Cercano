package theme

import (
	"fmt"
	"image/color"
	"math"

	"charm.land/lipgloss/v2"
)

// Hex converts a color.Color to #RRGGBB.
func Hex(c color.Color) string {
	rgb := RGB(c)
	return fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
}

// RGB converts a color.Color to 8-bit RGB channels.
func RGB(c color.Color) [3]uint8 {
	if c == nil {
		return [3]uint8{}
	}
	r, g, b, _ := c.RGBA()
	return [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}

// Color builds a lipgloss color from RGB channels.
func Color(c [3]uint8) color.Color {
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]))
}

// Fade scales a color's RGB toward black by factor k (0 = black, 1 = unchanged).
func Fade(c color.Color, k float64) color.Color {
	rgb := RGB(c)
	scale := func(v uint8) uint8 { return uint8(float64(v) * k) }
	return Color([3]uint8{scale(rgb[0]), scale(rgb[1]), scale(rgb[2])})
}

// Lerp returns the linear interpolation between two colors.
func Lerp(a, b color.Color, t float64) color.Color {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	ar := RGB(a)
	br := RGB(b)
	return Color([3]uint8{
		uint8(float64(ar[0]) + (float64(br[0])-float64(ar[0]))*t),
		uint8(float64(ar[1]) + (float64(br[1])-float64(ar[1]))*t),
		uint8(float64(ar[2]) + (float64(br[2])-float64(ar[2]))*t),
	})
}

// IsLight reports whether a color is visually light enough that dark text or
// darkened accent colors usually read better over it.
func IsLight(c color.Color) bool {
	rgb := RGB(c)
	luma := 0.2126*float64(rgb[0]) + 0.7152*float64(rgb[1]) + 0.0722*float64(rgb[2])
	return luma >= 170
}

// SelectionBackgroundSGR returns the SGR sequence that paints the active text
// selection background for the palette. The selection renderer reasserts this
// sequence around nested ANSI styles so existing syntax/user-prompt foregrounds
// remain intact.
func SelectionBackgroundSGR(p Palette) string {
	rgb := RGB(p.SelectionBg)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", rgb[0], rgb[1], rgb[2])
}

// ActivityColorAt returns the foreground color for one column in the live
// activity sweep.
func ActivityColorAt(p Palette, col int, sweepPos float64, tail float64) color.Color {
	dist := float64(col) - sweepPos
	if dist < 0 {
		dist = -dist
	}
	if dist >= tail {
		return p.ActivityBase
	}
	return Lerp(p.ActivityBase, p.ActivityPeak, 1.0-dist/tail)
}

// SpinnerColorAt returns the foreground color for the live spinner pulse.
func SpinnerColorAt(p Palette, pulse float64) color.Color {
	return Lerp(p.SpinnerBase, p.SpinnerPeak, pulse)
}

// ContrastText returns whichever candidate has better WCAG-style contrast
// against bg. The candidates are usually a light page color and a dark ink color.
func ContrastText(bg, a, b color.Color) color.Color {
	if contrastRatio(bg, a) >= contrastRatio(bg, b) {
		return a
	}
	return b
}

// MeterLabelForeground returns readable label text for the context meter over a
// specific cell background. The compacting label spans animated fill and empty
// cells, so contrast must be decided per cell rather than once per theme.
func MeterLabelForeground(p Palette, bg color.Color, onFill bool) color.Color {
	if onFill {
		return ContrastText(bg, p.BgDeep, p.MeterLabelOnFill)
	}
	if IsLight(p.BgDeep) {
		return ContrastText(bg, p.BgDeep, p.Primary)
	}
	return ContrastText(bg, p.Bright, p.BgDeep)
}

func contrastRatio(a, b color.Color) float64 {
	la := relativeLuminance(a)
	lb := relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(c color.Color) float64 {
	rgb := RGB(c)
	linear := func(v uint8) float64 {
		s := float64(v) / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(rgb[0]) + 0.7152*linear(rgb[1]) + 0.0722*linear(rgb[2])
}

func pow(x, y float64) float64 {
	return math.Pow(x, y)
}
