package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// TestConfigSurfaceFocusEntersAndExitsBody walks the focus ladder: the tab bar
// starts focused, Down drops into the body, Esc climbs back to the tab bar, and
// a second Esc closes the whole surface.
func TestConfigSurfaceFocusEntersAndExitsBody(t *testing.T) {
	m := Model{configSurface: &configSurface{active: configTabUI, focused: true}}

	m, _, handled := m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled || m.configSurface.focused {
		t.Fatalf("Down should enter the body: handled=%v focused=%v", handled, m.configSurface.focused)
	}

	m, _, handled = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || m.configSurface == nil || !m.configSurface.focused {
		t.Fatalf("Esc in body should return to the tab bar, not close: handled=%v", handled)
	}

	m, _, handled = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || m.configSurface != nil || m.content != nil {
		t.Fatalf("Esc on the tab bar should close the surface: handled=%v surface=%v", handled, m.configSurface)
	}
}

// TestConfigSurfaceUpAtFirstFieldReturnsToTabBar verifies the form-body climb:
// with the body focused and the form cursor on its first field, Up hands focus
// back to the tab bar instead of being swallowed by the form.
func TestConfigSurfaceUpAtFirstFieldReturnsToTabBar(t *testing.T) {
	themes, active := scopeTestThemes(t)
	sp, _ := newScopedSettingsPage(nil, active.Palette, theme.NewStyles(active.Palette), "palette:accent", 80, 40, themes, active, scopeUI)
	m := Model{content: sp, configSurface: &configSurface{active: configTabUI, focused: false}}

	m, _, handled := m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled || !m.configSurface.focused {
		t.Fatalf("Up at the first field should focus the tab bar: handled=%v", handled)
	}
}

// TestConfigSurfaceBodyKeysFallThrough confirms that, once in the body, a
// non-navigation key the surface doesn't own falls through to the page (so the
// form/dashboard/context viewer keep their own controls).
func TestConfigSurfaceBodyKeysFallThrough(t *testing.T) {
	// A settingsPage with a nil form: the Up-climb guard must not fire (and must
	// not panic), and a Down in the body must fall through unhandled.
	m := Model{content: &settingsPage{}, configSurface: &configSurface{active: configTabUI, focused: false}}
	if _, _, handled := m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyDown}); handled {
		t.Fatal("Down in the body should fall through to the page, not be consumed by the surface")
	}
	if _, _, handled := m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyUp}); handled {
		t.Fatal("Up with a nil form should fall through, not be consumed")
	}
}

// TestConfigSurfaceTabCyclesTabs verifies the reported fix: while the tab bar is
// focused, Tab advances to the next tab and Shift+Tab steps back — keeping focus
// on the strip so the user can keep cycling. (Cloud/UI/Models build safely with
// a nil agent, so no live server is needed.)
func TestConfigSurfaceTabCyclesTabs(t *testing.T) {
	m := Model{configSurface: &configSurface{active: configTabCloud, focused: true}}

	m, _, handled := m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if !handled || !m.configSurface.focused || m.configSurface.active != configTabModels {
		t.Fatalf("Tab on the strip should advance to the next tab and keep focus: handled=%v active=%v focused=%v",
			handled, m.configSurface.active, m.configSurface.focused)
	}

	m, _, handled = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if !handled || !m.configSurface.focused || m.configSurface.active != configTabCloud {
		t.Fatalf("Shift+Tab on the strip should step back to the previous tab: handled=%v active=%v",
			handled, m.configSurface.active)
	}
}

// TestConfigSurfaceShiftTabInBodyReturnsToTabBar covers the keyboard round-trip:
// once focus is in the body, Shift+Tab lifts it back to the tab strip so the
// tabs are always reachable without reaching for the mouse or Esc.
func TestConfigSurfaceShiftTabInBodyReturnsToTabBar(t *testing.T) {
	m := Model{content: &settingsPage{}, configSurface: &configSurface{active: configTabUI, focused: false}}

	m, _, handled := m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if !handled || !m.configSurface.focused {
		t.Fatalf("Shift+Tab in the body should lift focus back to the tab bar: handled=%v focused=%v",
			handled, m.configSurface.focused)
	}
}
