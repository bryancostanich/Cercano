package ui

import tea "charm.land/bubbletea/v2"

// configStripRows is how many screen rows the config tab strip occupies. The
// active page is sized one row shorter so the strip and the page together fill
// the same region a lone content page would.
const configStripRows = 1

// configSurface holds the state of the unified /config tabbed surface. When a
// Model carries a non-nil configSurface, m.content is the active tab's page and
// the tab strip renders directly above it. `active` is the selected tab;
// `focused` is true while the tab bar (rather than the page body) owns keyboard
// input — the state that decides whether ←/→ switch tabs or reach the body.
type configSurface struct {
	active  configTab
	focused bool
}

// contentPageHeight is the height budget handed to the active content page —
// the full terminal height, less the tab strip when the config surface is open.
func (m Model) contentPageHeight() int {
	if m.configSurface != nil {
		return m.height - configStripRows
	}
	return m.height
}

// configStripTop is the screen row the tab strip renders on: just below the
// header, its divider, and the optional splash. The page body begins one row
// lower (contentTop already accounts for the strip when the surface is open).
func (m Model) configStripTop() int {
	top := 2
	if m.splashEffective() {
		top += 9
	}
	return top
}

// openConfigSurface enters the tabbed config surface on the given tab with the
// tab bar focused, and returns the new page's init/refresh cmd.
func (m *Model) openConfigSurface(tab configTab) tea.Cmd {
	m.configSurface = &configSurface{active: clampConfigTab(tab), focused: true}
	page, cmd := m.buildConfigTabPage(m.configSurface.active)
	m.content = page
	return cmd
}

// closeConfigSurface tears the surface down and returns to chat. Mirrors the
// legacy page-close path by refreshing the header's model names, which a
// General/Cloud/Models edit may have changed.
func (m *Model) closeConfigSurface() tea.Cmd {
	m.configSurface = nil
	m.content = nil
	m.contentScrollbarDragging = false
	return fetchConfigCmd(m.agent)
}

// switchConfigTab rebuilds the page for a newly selected tab, leaving focus on
// the tab bar so ←/→ and Tab/Shift+Tab keep cycling. Returns the new page's
// init/refresh cmd.
func (m *Model) switchConfigTab(tab configTab) tea.Cmd {
	tab = clampConfigTab(tab)
	if m.configSurface == nil {
		return m.openConfigSurface(tab)
	}
	m.configSurface.active = tab
	m.configSurface.focused = true
	page, cmd := m.buildConfigTabPage(tab)
	m.content = page
	return cmd
}

// buildConfigTabPage constructs a fresh page for a tab, sized to leave room for
// the strip, batched with whatever cmd keeps it live (the dashboard and context
// view drive periodic refresh ticks). Pages are rebuilt on each switch — the
// same fresh-load behavior the standalone /m, /c, /config commands had — so a
// stale tab never lingers and the per-page refresh loops always restart.
func (m *Model) buildConfigTabPage(tab configTab) (contentPage, tea.Cmd) {
	h := m.contentPageHeight()
	switch tab {
	case configTabModels:
		d, cmd := newRuntimeDashboard(m.agent, m.palette, m.styles, m.width, h, dashboardModeModels)
		return d, tea.Batch(cmd, d.refreshTick())
	case configTabCloud:
		return newScopedSettingsPage(m.agent, m.palette, m.styles, m.promptColorToken, m.width, h, m.themes, m.theme, scopeCloud)
	case configTabRuntime:
		d, cmd := newRuntimeDashboard(m.agent, m.palette, m.styles, m.width, h, dashboardModeRuntime)
		return d, tea.Batch(cmd, d.refreshTick())
	case configTabUI:
		return newScopedSettingsPage(m.agent, m.palette, m.styles, m.promptColorToken, m.width, h, m.themes, m.theme, scopeUI)
	case configTabContext:
		cv, cmd := newContextView(m.agent, m.palette, m.styles, m.convID, m.width, h)
		return cv, tea.Batch(cmd, contextRefreshTick())
	default: // configTabGeneral
		return newScopedSettingsPage(m.agent, m.palette, m.styles, m.promptColorToken, m.width, h, m.themes, m.theme, scopeGeneral)
	}
}

// handleConfigSurfaceKey intercepts tab-navigation keys before the active page
// sees them, returning handled=true when it consumes the key. Focus model:
//
//   - Tab bar focused: ←/→ and Tab/Shift+Tab switch tabs (wrapping), 1–5 jump
//     to a tab, ↓/Enter drop into the body, Esc closes the surface.
//   - Body focused: Shift+Tab lifts focus back up to the tab bar (a reliable
//     keyboard path back to the tabs from any tab), Esc also steps back up (a
//     second Esc there closes), and ↑ at a form's first field climbs back to
//     the strip. Every other key falls through to the page.
//
// ←/→ are only claimed while the tab bar is focused, so a focused select field
// (which uses ←/→ to change its value) and the context viewer (←/→ expand and
// collapse) keep those keys once you drop into the body.
func (m Model) handleConfigSurfaceKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	cs := m.configSurface
	if cs == nil {
		return m, nil, false
	}

	key := msg.String()

	if key == "esc" {
		if !cs.focused {
			cs.focused = true
			return m, nil, true
		}
		cmd := m.closeConfigSurface()
		return m, cmd, true
	}

	if cs.focused {
		switch key {
		case "left", "shift+tab":
			return m, m.switchConfigTab(cycleConfigTab(cs.active, -1)), true
		case "right", "tab":
			return m, m.switchConfigTab(cycleConfigTab(cs.active, +1)), true
		case "down", "enter":
			cs.focused = false
			return m, nil, true
		case "1", "2", "3", "4", "5":
			return m, m.switchConfigTab(configTab(int(key[0] - '1'))), true
		}
		// The tab bar owns focus: swallow other keys so nothing leaks into the
		// body while the user is on the strip.
		return m, nil, true
	}

	// Body focused: Shift+Tab always lifts focus back to the tab strip, so there
	// is a dependable keyboard route back to the tabs from any tab's body.
	if key == "shift+tab" {
		cs.focused = true
		return m, nil, true
	}
	// ↑ at a settings form's first field also climbs back to the tab bar.
	if key == "up" {
		if sp, ok := m.content.(*settingsPage); ok && sp.form != nil && sp.form.Cursor() == 0 {
			cs.focused = true
			return m, nil, true
		}
	}
	return m, nil, false
}
