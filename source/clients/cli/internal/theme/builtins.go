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
		{Name: "meadow", Palette: meadowPalette()},
		{Name: "dawnwave", Palette: dawnwavePalette()},
	}
}

func phosphorPalette() Palette {
	return Palette{
		BgDeep: hc("#0A0F0A"), Surface: hc("#11180F"), BorderDim: hc("#1F3A1F"), Border: hc("#356635"),
		Primary: hc("#33FF33"), Bright: hc("#9CFF9C"), Dim: hc("#0E3A0E"), Accent: hc("#7CFF4D"),
		Info: hc("#33CFAF"), Muted: hc("#5C8C5C"), Success: hc("#6FCF6F"), Warn: hc("#CFCF4D"), Error: hc("#E86F6F"),
		SelectionBg: hc("#58829E"), CodeBlockBg: hc("#1A1A1A"), BypassText: hc("#0A0F0A"), ActivityBase: hc("#7CFF4D"), ActivityPeak: hc("#9CFF9C"), WordmarkPeak: hc("#9CFF9C"), SpinnerBase: hc("#33FF33"), SpinnerPeak: hc("#9CFF9C"), MeterLabelOnFill: hc("#0A0F0A"),
		BufferLink: hc("#33CFAF"), BufferCode: hc("#9CE0A0"), BufferLime: hc("#7CFF4D"), BufferError: hc("#D95C5C"), BufferUserBg: hc("#11331A"),
	}
}

func synthwavePalette() Palette {
	return Palette{
		BgDeep: hc("#1A1033"), Surface: hc("#241640"), BorderDim: hc("#3A2A66"), Border: hc("#6F4DBF"),
		Primary: hc("#FF6AD5"), Bright: hc("#FFC2F0"), Dim: hc("#4D2A66"), Accent: hc("#36E0E0"),
		Info: hc("#8A6AFF"), Muted: hc("#9B86C9"), Success: hc("#6FE0B0"), Warn: hc("#FFD24D"), Error: hc("#FF5C8A"),
		SelectionBg: hc("#58829E"), CodeBlockBg: hc("#1A1A1A"), BypassText: hc("#1A1033"), ActivityBase: hc("#36E0E0"), ActivityPeak: hc("#FFC2F0"), WordmarkPeak: hc("#FFC2F0"), SpinnerBase: hc("#FF6AD5"), SpinnerPeak: hc("#FFC2F0"), MeterLabelOnFill: hc("#1A1033"),
		BufferLink: hc("#36E0E0"), BufferCode: hc("#C2A6FF"), BufferLime: hc("#FF6AD5"), BufferError: hc("#FF5C8A"), BufferUserBg: hc("#2E2057"),
	}
}

func daylightPalette() Palette {
	return Palette{
		BgDeep: hc("#FBF3E0"), Surface: hc("#F1E6CC"), BorderDim: hc("#D8C7A0"), Border: hc("#B79A5E"),
		Primary: hc("#5A3A0A"), Bright: hc("#7A4E0A"), Dim: hc("#C9B68C"), Accent: hc("#1E7A3C"),
		Info: hc("#1763A0"), Muted: hc("#6F6A55"), Success: hc("#2E7D32"), Warn: hc("#8A5A00"), Error: hc("#B23A3A"),
		SelectionBg: hc("#58829E"), CodeBlockBg: hc("#1A1A1A"), BypassText: hc("#FBF3E0"), ActivityBase: hc("#1E7A3C"), ActivityPeak: hc("#104321"), WordmarkPeak: hc("#7A4E0A"), SpinnerBase: hc("#5A3A0A"), SpinnerPeak: hc("#7A4E0A"), MeterLabelOnFill: hc("#5A3A0A"),
		BufferLink: hc("#1763A0"), BufferCode: hc("#6A4BA3"), BufferLime: hc("#1E7A3C"), BufferError: hc("#B23A3A"), BufferUserBg: hc("#E6D7B0"),
	}
}

// meadowPalette is the light inverse of phosphor: deep green ink on pale green
// paper. Same green/teal hue family as phosphor, values flipped for a light bg
// (dark Primary/Bright text, pale Dim/BufferUserBg, accents darkened to read).
func meadowPalette() Palette {
	return Palette{
		BgDeep: hc("#EDF6E9"), Surface: hc("#E0EED9"), BorderDim: hc("#C4DCB8"), Border: hc("#8DBE80"),
		Primary: hc("#14571A"), Bright: hc("#1F7A28"), Dim: hc("#B6D3AA"), Accent: hc("#3C7A1E"),
		Info: hc("#1A7A6E"), Muted: hc("#5F704F"), Success: hc("#2E7D32"), Warn: hc("#805E00"), Error: hc("#B23A3A"),
		SelectionBg: hc("#58829E"), CodeBlockBg: hc("#1A1A1A"), BypassText: hc("#EDF6E9"), ActivityBase: hc("#3C7A1E"), ActivityPeak: hc("#214310"), WordmarkPeak: hc("#1F7A28"), SpinnerBase: hc("#14571A"), SpinnerPeak: hc("#1F7A28"), MeterLabelOnFill: hc("#14571A"),
		BufferLink: hc("#1A7A6E"), BufferCode: hc("#5A6A24"), BufferLime: hc("#3C7A1E"), BufferError: hc("#B23A3A"), BufferUserBg: hc("#DAEAD1"),
	}
}

// dawnwavePalette is the light inverse of synthwave: magenta/violet ink and
// cyan accents on pale lavender. Same magenta/cyan/violet hue family as
// synthwave, values flipped for a light bg.
func dawnwavePalette() Palette {
	return Palette{
		BgDeep: hc("#F7EEFA"), Surface: hc("#EEDFF5"), BorderDim: hc("#D9BFE7"), Border: hc("#B98FD1"),
		Primary: hc("#7A2A6A"), Bright: hc("#A62E80"), Dim: hc("#D2B6DE"), Accent: hc("#0B6D85"),
		Info: hc("#6A3FC0"), Muted: hc("#7A6685"), Success: hc("#2A6E50"), Warn: hc("#8A5600"), Error: hc("#C13060"),
		SelectionBg: hc("#58829E"), CodeBlockBg: hc("#1A1A1A"), BypassText: hc("#F7EEFA"), ActivityBase: hc("#0B6D85"), ActivityPeak: hc("#07495A"), WordmarkPeak: hc("#A62E80"), SpinnerBase: hc("#7A2A6A"), SpinnerPeak: hc("#A62E80"), MeterLabelOnFill: hc("#7A2A6A"),
		BufferLink: hc("#0B6D85"), BufferCode: hc("#7A46BE"), BufferLime: hc("#B0308A"), BufferError: hc("#C13060"), BufferUserBg: hc("#ECD8F0"),
	}
}
