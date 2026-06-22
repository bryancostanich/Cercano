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

// Cracker returns the default cercano-cli palette. Hex codes match
// `conductor/tracks/cli/spec.md §6.1`.
func Cracker() Palette {
	return Palette{
		BgDeep:    lipgloss.Color("#1A1A1A"),
		Surface:   lipgloss.Color("#252525"),
		BorderDim: lipgloss.Color("#434343"),
		Border:    lipgloss.Color("#6F6F6F"),
		Primary:   lipgloss.Color("#EA8212"),
		Bright:    lipgloss.Color("#FFB84D"),
		DimAmber:  lipgloss.Color("#5A3308"),
		Accent:    lipgloss.Color("#BDF000"),
		Info:      lipgloss.Color("#00C8E8"),
		Muted:     lipgloss.Color("#888888"),
		Success:   lipgloss.Color("#6FCF6F"),
		Warn:      lipgloss.Color("#FFD24D"),
		Error:     lipgloss.Color("#E84D4D"),
	}
}
