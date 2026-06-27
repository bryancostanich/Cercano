package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// settingsColorMsg is emitted when the accent-color field commits. The root
// model resolves the token and updates promptBorderColor (CLI-local, mirrors
// ResultSetPromptColor).
type settingsColorMsg struct{ token string }

// settingsPage is the sectioned settings content page (opened by /s, /settings,
// /config). It replaces the old flat configEditor.
type settingsPage struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	accentToken   string
	form          *form.Form
	offset        int
}

func newSettingsPage(ag *agentclient.Client, p theme.Palette, s theme.Styles, accentToken string, w, h int) (*settingsPage, tea.Cmd) {
	sp := &settingsPage{agent: ag, palette: p, styles: s, accentToken: accentToken, width: w, height: h}
	sp.form = form.New(sp.snapshotSections())
	sp.form.OnCommit = sp.onCommit
	sp.form.OnReload = sp.snapshotSections
	return sp, nil
}

func (sp *settingsPage) snapshotSections() []form.Section {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg, err := sp.agent.GetConfig(ctx)
	if err != nil {
		return []form.Section{{Title: "Settings", Fields: []form.Field{
			form.NewReadOnly("error", "error", err.Error(), ""),
		}}}
	}
	mode, err := sp.agent.GetPermissionMode(ctx)
	if err != nil {
		mode = ""
	}
	return buildSettingsSections(cfg, mode, sp.accentToken)
}

func (sp *settingsPage) ID() contentPageID { return contentPageSettings }

func (sp *settingsPage) SetSize(w, h int) { sp.width, sp.height = w, h }

func (sp *settingsPage) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	cmd, closed := sp.form.Update(msg)
	sp.clampScroll()
	return cmd, closed
}

func (sp *settingsPage) View() string {
	lines := sp.form.Lines(sp.width, sp.palette, sp.styles)
	sp.clampScroll()
	return renderScrollable(lines, sp.height, sp.width-2, sp.offset, sp.styles)
}

// onCommit routes a committed field to its sink.
func (sp *settingsPage) onCommit(key, value string) (string, tea.Cmd, error) {
	action := classifyCommit(key, value)
	switch action.kind {
	case commitConfig:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status, err := sp.agent.UpdateConfig(ctx, action.update)
		return status, nil, err
	case commitPermission:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sp.agent.SetPermissionMode(ctx, action.value); err != nil {
			return "", nil, err
		}
		mode := action.value
		return "permission mode: " + mode, func() tea.Msg {
			return permissionModeChangedMsg{mode: mode}
		}, nil
	case commitColor:
		sp.accentToken = action.value
		token := action.value
		return "accent color set", func() tea.Msg {
			return settingsColorMsg{token: token}
		}, nil
	}
	return "", nil, nil
}

// --- contentPageScroller ---

func (sp *settingsPage) ScrollBy(delta int) { sp.offset += delta; sp.clampScroll() }
func (sp *settingsPage) ScrollTo(offset int) { sp.offset = offset; sp.clampScroll() }
func (sp *settingsPage) ScrollState() contentPageScrollState {
	total := len(sp.form.Lines(sp.width, sp.palette, sp.styles))
	return contentPageScrollState{Total: total, Height: sp.height, Offset: sp.offset}
}

func (sp *settingsPage) clampScroll() {
	total := len(sp.form.Lines(sp.width, sp.palette, sp.styles))
	max := total - sp.height
	if max < 0 {
		max = 0
	}
	if sp.offset > max {
		sp.offset = max
	}
	if sp.offset < 0 {
		sp.offset = 0
	}
}
