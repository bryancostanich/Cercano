// Package theme defines the cercano-cli color palette and pre-built lipgloss
// styles. The default theme is "cracker" — amber + lime on charcoal, evoking
// 80s phosphor terminals and demoscene cracker intros.
package theme

import "github.com/charmbracelet/lipgloss"

// Palette is the set of named colors a theme exposes.
type Palette struct {
	BgDeep      lipgloss.Color // terminal background fallback
	Surface     lipgloss.Color // overlay panel background
	BorderDim   lipgloss.Color // outer chrome lines
	Border      lipgloss.Color // inner gridlines
	Primary     lipgloss.Color // wordmark, default text
	Bright      lipgloss.Color // active state, focus
	DimAmber    lipgloss.Color // meter empty, ghost text
	Accent      lipgloss.Color // lime — user sigil, accent rail, success peak
	Info        lipgloss.Color // cyan — metadata, paths
	Muted       lipgloss.Color // secondary text
	Success     lipgloss.Color // confirmations, build pass
	Warn        lipgloss.Color // meter mid-range, advisory
	Error       lipgloss.Color // failures, bypass indicator
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
