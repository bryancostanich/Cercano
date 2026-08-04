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
	case configTabMcp:
		d, cmd := newMcpDashboard(m.agent, m.palette, m.styles, m.width, h)
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
//     to a tab, Enter drops into the body, ↓ drops into the body and is also
//     forwarded to the page so a list moves its cursor on that first press,
//     Esc closes the surface.
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
			// The body owns focus. If the active page has a transient overlay
			// open (e.g. the MCP dashboard's details / add-server popover), let
			// Esc close that overlay first rather than yanking focus back to the
			// tab strip. Only once the page has nothing left to dismiss does Esc
			// step focus back up.
			if m.pageWantsEscape() {
				next, cmd := m.dropFocusForwarding(msg)
				return next, cmd, true
			}
			cs.focused = true
			m.blurContentBody()
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
		case "enter":
			// Enter just commits focus into the body without moving anything.
			cs.focused = false
			return m, nil, true
		case "down":
			// Drop focus into the body AND forward this same ↓ so a list page
			// (e.g. the MCP dashboard) moves its cursor on the first press,
			// instead of the keystroke being silently swallowed to transfer
			// focus. For a settings form the forwarded ↓ advances off field 0,
			// which reads naturally too.
			next, cmd := m.dropFocusForwarding(msg)
			return next, cmd, true
		case "1", "2", "3", "4", "5", "6", "7":
			return m, m.switchConfigTab(configTab(int(key[0] - '1'))), true
		}
		// A page may advertise action hotkeys (e.g. the MCP dashboard's
		// a/r/x) that should work even from the strip: drop into the body and
		// forward the key so the hint row's promise holds on the first press.
		if m.pageWantsStripKey(key) {
			next, cmd := m.dropFocusForwarding(msg)
			return next, cmd, true
		}
		// The tab bar owns focus: swallow other keys so nothing leaks into the
		// body while the user is on the strip.
		return m, nil, true
	}

	// Body focused: Shift+Tab always lifts focus back to the tab strip, so there
	// is a dependable keyboard route back to the tabs from any tab's body.
	if key == "shift+tab" {
		cs.focused = true
		m.blurContentBody()
		return m, nil, true
	}
	// ↑ at a settings form's first field also climbs back to the tab bar.
	if key == "up" {
		if sp, ok := m.content.(*settingsPage); ok && sp.form != nil && sp.form.Cursor() == 0 {
			cs.focused = true
			m.blurContentBody()
			return m, nil, true
		}
	}
	return m, nil, false
}

// dropFocusForwarding lifts focus from the tab strip into the body and forwards
// the triggering key to the active page in the same step, so a list moves its
// cursor or fires an action hotkey on that first press rather than spending the
// keystroke solely on the focus transfer.
func (m Model) dropFocusForwarding(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.configSurface != nil {
		m.configSurface.focused = false
	}
	if m.content == nil {
		return m, nil
	}
	cmd, _ := m.content.Update(msg)
	return m, cmd
}

// pageWantsStripKey reports whether the active page has asked for this key to be
// forwarded from the tab strip (via stripForwardingPage).
func (m Model) pageWantsStripKey(key string) bool {
	p, ok := m.content.(stripForwardingPage)
	if !ok {
		return false
	}
	for _, k := range p.stripForwardKeys() {
		if k == key {
			return true
		}
	}
	return false
}

// pageWantsEscape reports whether the active page has a transient overlay open
// that Esc should dismiss before the config surface treats Esc as "step focus
// back to the tab strip". Pages without dismissable overlays don't implement
// escapeConsumingPage and this returns false.
func (m Model) pageWantsEscape() bool {
	p, ok := m.content.(escapeConsumingPage)
	return ok && p.wantsEscape()
}

// escapeConsumingPage is a content page that can have a transient overlay open
// (details popover, add form, …) which Esc should close before the config
// surface reinterprets Esc as a focus/close step.
type escapeConsumingPage interface {
	wantsEscape() bool
}

// pasteConsumingPage is a content page with a text field that can accept a
// bracketed paste. The page returns true when it consumed the text; otherwise
// the paste is dropped (content pages don't feed the prompt input).
type pasteConsumingPage interface {
	handlePaste(text string) bool
}

// bodyFocusablePage is a content page that tracks whether the config surface's
// body (vs. the tab strip) owns the keyboard, so it can suppress its cursor
// marker while focus is up on the strip.
type bodyFocusablePage interface {
	blurBody()
}

// stripForwardingPage is a content page that declares keys which, while the tab
// strip owns focus, should drop into the body and be forwarded to the page —
// used for dashboard action hotkeys advertised in the page's hint row.
type stripForwardingPage interface {
	stripForwardKeys() []string
}

// blurContentBody tells the active content page that focus has lifted back to
// the tab strip, so any cursor marker it draws should be hidden until the body
// is re-entered. A no-op for pages that don't implement bodyFocusablePage.
func (m *Model) blurContentBody() {
	if p, ok := m.content.(bodyFocusablePage); ok {
		p.blurBody()
	}
}
