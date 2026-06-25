package theme

import "charm.land/lipgloss/v2"

// Styles are pre-built lipgloss styles derived from a Palette. Build once at
// startup; reuse on every render. All styles inherit from the palette so a
// future theme switch is a single Palette swap.
type Styles struct {
	Primary    lipgloss.Style
	Bright     lipgloss.Style
	Dim        lipgloss.Style
	Accent     lipgloss.Style
	Info       lipgloss.Style
	Muted      lipgloss.Style
	Border     lipgloss.Style
	BorderDim  lipgloss.Style
	Success    lipgloss.Style
	Warn       lipgloss.Style
	Error      lipgloss.Style

	UserPrompt  lipgloss.Style // lime ▶ prefix for the live input line
	AgentProse  lipgloss.Style // default assistant text

	BufferCode       lipgloss.Style // muted lavender — code-fence lang in scrollback (echoes inline code)
	BufferUserPrompt lipgloss.Style // muted lime ▶ for echoed user input in scrollback
	BufferUserLine   lipgloss.Style // navy background fill behind echoed user prompt lines
	BufferUserMarker lipgloss.Style // muted lime ▶ on the navy fill
	MeterFill   lipgloss.Style // lime block
	MeterEmpty  lipgloss.Style // dim-amber block
	BypassFlag  lipgloss.Style // red ! BYPASS block
}

// NewStyles builds a Styles bundle from a Palette.
func NewStyles(p Palette) Styles {
	return Styles{
		Primary:   lipgloss.NewStyle().Foreground(p.Primary),
		Bright:    lipgloss.NewStyle().Foreground(p.Bright),
		Dim:       lipgloss.NewStyle().Foreground(p.DimAmber),
		Accent:    lipgloss.NewStyle().Foreground(p.Accent),
		Info:      lipgloss.NewStyle().Foreground(p.Info),
		Muted:     lipgloss.NewStyle().Foreground(p.Muted),
		Border:    lipgloss.NewStyle().Foreground(p.Border),
		BorderDim: lipgloss.NewStyle().Foreground(p.BorderDim),
		Success:   lipgloss.NewStyle().Foreground(p.Success),
		Warn:      lipgloss.NewStyle().Foreground(p.Warn),
		Error:     lipgloss.NewStyle().Foreground(p.Error),

		UserPrompt: lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		AgentProse: lipgloss.NewStyle().Foreground(p.Primary),

		BufferCode:       lipgloss.NewStyle().Foreground(BufferCode),
		BufferUserPrompt: lipgloss.NewStyle().Foreground(BufferLime).Bold(true),
		BufferUserLine:   lipgloss.NewStyle().Background(BufferUserBg),
		BufferUserMarker: lipgloss.NewStyle().Foreground(BufferLime).Background(BufferUserBg).Bold(true),
		MeterFill:  lipgloss.NewStyle().Foreground(p.Accent),
		MeterEmpty: lipgloss.NewStyle().Foreground(p.DimAmber),
		BypassFlag: lipgloss.NewStyle().Foreground(lipgloss.Color("#1A1A1A")).Background(p.Error).Bold(true),
	}
}
