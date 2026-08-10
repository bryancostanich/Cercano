package ui

import (
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
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
	// The bare-struct fixture skips the constructor — focus changes
	// call catalogSearch.Focus(), which needs a real textinput.
	d.catalogSearch = textinput.New()
	return d
}

// The Models-mode fixture yields two action sections: installed models
// (start + delete for the downloaded model) and model tiers (every slot).
// The open-model / runtime picker now lives on the Runtime tab, and
// downloads and processes are empty here so both are skipped by tab.
func TestTabWalksSections(t *testing.T) {
	d := focusNavTestDashboard()
	starts := d.sectionStarts()
	if len(starts) != 2 {
		t.Fatalf("sectionStarts = %v, want [installed, tiers]", starts)
	}
	tab := tea.KeyPressMsg{Code: tea.KeyTab}

	// Stops on the Models tab: filter -> list -> installed -> tiers -> filter.
	d.Update(tab) // filter -> list
	if d.focus != runtimeFocusList {
		t.Fatalf("after 1 tab: focus=%v, want list", d.focus)
	}
	d.Update(tab) // list -> installed models
	if d.focus != runtimeFocusActions || d.operationCursor != starts[0] {
		t.Fatalf("after 2 tabs: focus=%v cursor=%d, want actions@%d", d.focus, d.operationCursor, starts[0])
	}
	d.Update(tab) // -> model tiers
	if d.operationCursor != starts[1] {
		t.Fatalf("after 3 tabs: cursor=%d, want tiers start %d", d.operationCursor, starts[1])
	}
	if d.scrollOffset == 0 {
		t.Fatal("tabbing into tiers should scroll the viewport down")
	}
	d.Update(tab) // wraps back to the filter
	if d.focus != runtimeFocusFilter {
		t.Fatal("tab past the last section should return to the filter")
	}
}

func TestShiftTabWalksSectionsInReverse(t *testing.T) {
	d := focusNavTestDashboard()
	starts := d.sectionStarts()
	shiftTab := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

	// Reverse stops: filter -> tiers -> installed -> list -> filter.
	d.Update(shiftTab) // filter wraps to the LAST section — model tiers
	if d.focus != runtimeFocusActions || d.operationCursor != starts[len(starts)-1] {
		t.Fatalf("shift+tab from filter: focus=%v cursor=%d, want tiers start %d", d.focus, d.operationCursor, starts[len(starts)-1])
	}
	d.Update(shiftTab) // -> installed models
	if d.operationCursor != starts[0] {
		t.Fatalf("second shift+tab: cursor=%d, want installed start %d", d.operationCursor, starts[0])
	}
	d.Update(shiftTab) // -> list
	if d.focus != runtimeFocusList {
		t.Fatalf("third shift+tab: focus=%v, want list", d.focus)
	}
	d.Update(shiftTab) // -> back out to filter
	if d.focus != runtimeFocusFilter {
		t.Fatal("shift+tab past the list should return to the filter")
	}
}

// The Runtime tab never renders a catalog block (fullContent's mode guard),
// so it must never start — or land back on — catalog focus: doing so would
// route arrow keys/Enter to an invisible model list instead of the visible
// runtime picker, and Enter there fires a real download (see
// newRuntimeDashboard's mode comment).
func TestNewRuntimeDashboard_RuntimeModeStartsOnActions(t *testing.T) {
	d, _ := newRuntimeDashboard(nil, theme.Palette{}, theme.Styles{}, 80, 24, dashboardModeRuntime)
	if d.focus != runtimeFocusActions {
		t.Fatalf("Runtime-mode dashboard focus = %v, want runtimeFocusActions", d.focus)
	}
}

func TestNewRuntimeDashboard_ModelsModeStartsOnFilter(t *testing.T) {
	d, _ := newRuntimeDashboard(nil, theme.Palette{}, theme.Styles{}, 80, 24, dashboardModeModels)
	if d.focus != runtimeFocusFilter {
		t.Fatalf("Models-mode dashboard focus = %v, want runtimeFocusFilter (search box focused)", d.focus)
	}
}

// In Runtime mode there is exactly one action section (the runtime/open-model
// picker), so Tab past its end must wrap within actions, not fall back to the
// nonexistent catalog.
func TestAdvanceSection_RuntimeModeWrapsWithinActions(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{Config: &agentclient.Config{}})
	d.mode = dashboardModeRuntime
	d.focus = runtimeFocusActions
	d.catalogSearch = textinput.New()

	starts := d.sectionStarts()
	if len(starts) == 0 {
		t.Fatal("expected at least one action section in runtime mode")
	}
	d.operationCursor = starts[len(starts)-1]

	d.advanceSection(1)
	if d.focus != runtimeFocusActions {
		t.Fatalf("focus after wrap = %v, want runtimeFocusActions (never catalog in runtime mode)", d.focus)
	}
	if d.operationCursor != starts[0] {
		t.Fatalf("operationCursor after wrap = %d, want %d (first section)", d.operationCursor, starts[0])
	}

	d.advanceSection(-1)
	if d.focus != runtimeFocusActions {
		t.Fatalf("focus after reverse wrap = %v, want runtimeFocusActions", d.focus)
	}
	if d.operationCursor != starts[len(starts)-1] {
		t.Fatalf("operationCursor after reverse wrap = %d, want %d (last section)", d.operationCursor, starts[len(starts)-1])
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
