// Package theme defines the cercano-cli color palette and pre-built lipgloss
// styles. The default theme is "cracker" — amber + lime on charcoal, evoking
// 80s phosphor terminals and demoscene cracker intros.
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette is the set of named colors a theme exposes.
type Palette struct {
	BgDeep      color.Color // terminal background fallback
	Surface     color.Color // overlay panel background
	BorderDim   color.Color // outer chrome lines
	Border      color.Color // inner gridlines
	Primary     color.Color // wordmark, default text
	Bright      color.Color // active state, focus
	DimAmber    color.Color // meter empty, ghost text
	Accent      color.Color // lime — user sigil, accent rail, success peak
	Info        color.Color // cyan — metadata, paths
	Muted       color.Color // secondary text
	Success     color.Color // confirmations, build pass
	Warn        color.Color // meter mid-range, advisory
	Error       color.Color // failures, bypass indicator
}

// Cracker palette hex codes — the single source of truth for both the lipgloss
// Palette (chrome) and the Glamour markdown style (assistant prose). Anything
// that needs a cracker color references these, never a bare literal. Match
// `docs/features/cli/README.md` ("Visual design").
const (
	hexBgDeep    = "#1A1A1A"
	hexSurface   = "#252525"
	hexBorderDim = "#434343"
	hexBorder    = "#6F6F6F"
	hexPrimary   = "#EA8212"
	hexBright    = "#FFB84D"
	hexDimAmber  = "#5A3308"
	hexAccent    = "#BDF000"
	hexInfo      = "#00C8E8"
	hexMuted     = "#888888"
	hexSuccess   = "#6FCF6F"
	hexWarn      = "#FFD24D"
	hexError     = "#E84D4D"
)

// Cracker returns the default cercano-cli palette, built from the hex constants
// above.
func Cracker() Palette {
	return Palette{
		BgDeep:    lipgloss.Color(hexBgDeep),
		Surface:   lipgloss.Color(hexSurface),
		BorderDim: lipgloss.Color(hexBorderDim),
		Border:    lipgloss.Color(hexBorder),
		Primary:   lipgloss.Color(hexPrimary),
		Bright:    lipgloss.Color(hexBright),
		DimAmber:  lipgloss.Color(hexDimAmber),
		Accent:    lipgloss.Color(hexAccent),
		Info:      lipgloss.Color(hexInfo),
		Muted:     lipgloss.Color(hexMuted),
		Success:   lipgloss.Color(hexSuccess),
		Warn:      lipgloss.Color(hexWarn),
		Error:     lipgloss.Color(hexError),
	}
}

// Buffer-muted accents. The main scrollback buffer reuses the chrome accent
// hues (cyan, lime, red) but at lower saturation, so rendered turns read calmer
// than the top bar / footer, which keep the full-saturation Palette colors.
// Same hue as the palette accents with the edge taken off; amber (Primary)
// stays the buffer's base color and is intentionally not muted.
const (
	bufLinkHex = "#2EA8BC" // muted cyan  — markdown links
	bufCodeHex = "#B7A6E0" // muted lavender — inline code, code-fence lang (distinct from cyan links)
	bufLimeHex = "#A9CE21" // muted Accent — tool ✓, focus caret, echoed user ▶
	bufRedHex  = "#D95C5C" // muted Error  — tool ⚠
	bufUserBgHex = "#1F4163" // muted navy — fill behind echoed user prompts in scrollback
)

// Buffer-muted lipgloss colors derived from the hexes above. Exported so the
// ui package can color scrollback content without reaching into chrome styles.
var (
	BufferLink  = lipgloss.Color(bufLinkHex)
	BufferCode  = lipgloss.Color(bufCodeHex)
	BufferLime  = lipgloss.Color(bufLimeHex)
	BufferError = lipgloss.Color(bufRedHex)
	BufferUserBg = lipgloss.Color(bufUserBgHex)
)
