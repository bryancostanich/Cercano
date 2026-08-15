package theme

import "charm.land/lipgloss/v2"

// Styles are pre-built lipgloss styles derived from a Palette. Build once at
// startup; reuse on every render. All styles inherit from the palette so a
// future theme switch is a single Palette swap.
type Styles struct {
	Primary   lipgloss.Style
	Bright    lipgloss.Style
	Dim       lipgloss.Style
	Accent    lipgloss.Style
	Info      lipgloss.Style
	Muted     lipgloss.Style
	Border    lipgloss.Style
	BorderDim lipgloss.Style
	Success   lipgloss.Style
	Warn      lipgloss.Style
	Error     lipgloss.Style

	UserPrompt lipgloss.Style // lime ▶ prefix for the live input line
	AgentProse lipgloss.Style // default assistant text

	BufferCode       lipgloss.Style // muted lavender — code-fence lang in scrollback (echoes inline code)
	BufferCodeBlock  lipgloss.Style // explicit dark canvas behind code-fence bodies
	BufferUserPrompt lipgloss.Style // muted lime ▶ for echoed user input in scrollback
	BufferUserLine   lipgloss.Style // navy background fill behind echoed user prompt lines
	BufferUserMarker lipgloss.Style // muted lime ▶ on the navy fill
	BufferUserText   lipgloss.Style // bright amber text on the navy user-prompt fill
	MeterFill        lipgloss.Style // lime block
	MeterEmpty       lipgloss.Style // dim-amber block
	BypassFlag       lipgloss.Style // red ! BYPASS block

	ToolSuccess lipgloss.Style // muted lime ✓ glyph
	ToolError   lipgloss.Style // muted red ⚠ glyph
	ToolFocus   lipgloss.Style // muted lime ▶ nav caret
}

// NewStyles builds a Styles bundle from a Palette.
func NewStyles(p Palette) Styles {
	return Styles{
		Primary:   lipgloss.NewStyle().Foreground(p.Primary),
		Bright:    lipgloss.NewStyle().Foreground(p.Bright),
		Dim:       lipgloss.NewStyle().Foreground(p.Dim),
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

		BufferCode:       lipgloss.NewStyle().Foreground(p.BufferCode),
		BufferCodeBlock:  lipgloss.NewStyle().Background(p.CodeBlockBg),
		BufferUserPrompt: lipgloss.NewStyle().Foreground(p.BufferLime).Bold(true),
		BufferUserLine:   lipgloss.NewStyle().Background(p.BufferUserBg),
		BufferUserMarker: lipgloss.NewStyle().Foreground(p.BufferLime).Background(p.BufferUserBg).Bold(true),
		BufferUserText:   lipgloss.NewStyle().Foreground(p.Bright).Background(p.BufferUserBg),
		MeterFill:        lipgloss.NewStyle().Foreground(p.Accent),
		MeterEmpty:       lipgloss.NewStyle().Foreground(p.Dim),
		BypassFlag:       lipgloss.NewStyle().Foreground(p.BypassText).Background(p.Error).Bold(true),
		ToolSuccess:      lipgloss.NewStyle().Foreground(p.BufferLime),
		ToolError:        lipgloss.NewStyle().Foreground(p.BufferError),
		ToolFocus:        lipgloss.NewStyle().Foreground(p.BufferLime),
	}
}
