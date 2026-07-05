package ui

import (
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

func focusNavTestDashboard() *runtimeDashboard {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Config: &agentclient.Config{},
		Status: &agentclient.RuntimeStatus{
			Models: []agentclient.RuntimeModel{{
				ID: "llama_server:catalog:m", DisplayName: "M",
				Runtime: "llama_server", DownloadState: "downloaded", Path: "/x/m.gguf",
			}},
		},
	})
	d.height = 24 // short viewport: tiers sit below the fold
	// The bare-struct fixture skips the constructor — toggling focus
	// calls catalogSearch.Focus(), which needs a real textinput.
	d.catalogSearch = textinput.New()
	return d
}

func TestShiftTabTogglesFocusBothWays(t *testing.T) {
	d := focusNavTestDashboard()
	if d.focus != runtimeFocusCatalog {
		t.Fatal("fixture should start on catalog")
	}
	shiftTab := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	d.Update(shiftTab)
	if d.focus != runtimeFocusActions {
		t.Fatal("shift+tab should move catalog -> actions")
	}
	d.Update(shiftTab)
	if d.focus != runtimeFocusCatalog {
		t.Fatal("shift+tab should move actions -> catalog")
	}
}

func TestActionCursorScrollFollowsIntoTiers(t *testing.T) {
	d := focusNavTestDashboard()
	d.focus = runtimeFocusActions

	// Jump to the last action row — a model-tiers row, far below the
	// fold on this short viewport.
	d.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if d.selectedActionLine < 0 {
		t.Fatal("selected action line not tracked during render")
	}
	_, contentH := d.fullContent()
	if d.scrollOffset == 0 {
		t.Fatalf("viewport did not scroll (selected line %d, height %d)", d.selectedActionLine, contentH)
	}
	if d.selectedActionLine < d.scrollOffset || d.selectedActionLine >= d.scrollOffset+contentH {
		t.Fatalf("selected line %d outside viewport [%d, %d)", d.selectedActionLine, d.scrollOffset, d.scrollOffset+contentH)
	}

	// And Home brings the viewport back up.
	d.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if d.selectedActionLine < d.scrollOffset || d.selectedActionLine >= d.scrollOffset+contentH {
		t.Fatalf("after home: selected line %d outside viewport [%d, %d)", d.selectedActionLine, d.scrollOffset, d.scrollOffset+contentH)
	}
}
