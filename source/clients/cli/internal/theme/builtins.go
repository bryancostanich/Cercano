package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

func hc(s string) color.Color { return lipgloss.Color(s) }

// BuiltinThemes returns the always-present, read-only themes, default (cr4k3r_j4x) first.
func BuiltinThemes() []Theme {
	return []Theme{
		{Name: "cr4k3r_j4x", Palette: Cracker()},
		{Name: "phosphor", Palette: phosphorPalette()},
		{Name: "synthwave", Palette: synthwavePalette()},
		{Name: "daylight", Palette: daylightPalette()},
	}
}

func phosphorPalette() Palette {
	return Palette{
		BgDeep: hc("#0A0F0A"), Surface: hc("#11180F"), BorderDim: hc("#1F3A1F"), Border: hc("#356635"),
		Primary: hc("#33FF33"), Bright: hc("#9CFF9C"), Dim: hc("#0E3A0E"), Accent: hc("#7CFF4D"),
		Info: hc("#33CFAF"), Muted: hc("#5C8C5C"), Success: hc("#6FCF6F"), Warn: hc("#CFCF4D"), Error: hc("#E86F6F"),
		BufferLink: hc("#33CFAF"), BufferCode: hc("#9CE0A0"), BufferLime: hc("#7CFF4D"), BufferError: hc("#D95C5C"), BufferUserBg: hc("#11331A"),
	}
}

func synthwavePalette() Palette {
	return Palette{
		BgDeep: hc("#1A1033"), Surface: hc("#241640"), BorderDim: hc("#3A2A66"), Border: hc("#6F4DBF"),
		Primary: hc("#FF6AD5"), Bright: hc("#FFC2F0"), Dim: hc("#4D2A66"), Accent: hc("#36E0E0"),
		Info: hc("#8A6AFF"), Muted: hc("#9B86C9"), Success: hc("#6FE0B0"), Warn: hc("#FFD24D"), Error: hc("#FF5C8A"),
		BufferLink: hc("#36E0E0"), BufferCode: hc("#C2A6FF"), BufferLime: hc("#FF6AD5"), BufferError: hc("#FF5C8A"), BufferUserBg: hc("#2E2057"),
	}
}

func daylightPalette() Palette {
	return Palette{
		BgDeep: hc("#FBF3E0"), Surface: hc("#F1E6CC"), BorderDim: hc("#D8C7A0"), Border: hc("#B79A5E"),
		Primary: hc("#5A3A0A"), Bright: hc("#7A4E0A"), Dim: hc("#C9B68C"), Accent: hc("#1E7A3C"),
		Info: hc("#1763A0"), Muted: hc("#8A7A55"), Success: hc("#2E7D32"), Warn: hc("#B8860B"), Error: hc("#B23A3A"),
		BufferLink: hc("#1763A0"), BufferCode: hc("#6A4BA3"), BufferLime: hc("#1E7A3C"), BufferError: hc("#B23A3A"), BufferUserBg: hc("#E6D7B0"),
	}
}
