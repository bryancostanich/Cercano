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

	d.Update(tab) // catalog -> installed models
	if d.focus != runtimeFocusActions || d.operationCursor != starts[0] {
		t.Fatalf("after 1 tab: focus=%v cursor=%d, want actions@%d", d.focus, d.operationCursor, starts[0])
	}
	d.Update(tab) // -> model tiers
	if d.operationCursor != starts[1] {
		t.Fatalf("after 2 tabs: cursor=%d, want tiers start %d", d.operationCursor, starts[1])
	}
	if d.scrollOffset == 0 {
		t.Fatal("tabbing into tiers should scroll the viewport down")
	}
	d.Update(tab) // wraps back to catalog
	if d.focus != runtimeFocusCatalog {
		t.Fatal("tab past the last section should return to the catalog")
	}
}

func TestShiftTabWalksSectionsInReverse(t *testing.T) {
	d := focusNavTestDashboard()
	starts := d.sectionStarts()
	shiftTab := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

	d.Update(shiftTab) // catalog wraps to the LAST section — model tiers
	if d.focus != runtimeFocusActions || d.operationCursor != starts[len(starts)-1] {
		t.Fatalf("shift+tab from catalog: focus=%v cursor=%d, want tiers start %d", d.focus, d.operationCursor, starts[len(starts)-1])
	}
	d.Update(shiftTab) // -> installed models
	if d.operationCursor != starts[0] {
		t.Fatalf("second shift+tab: cursor=%d, want installed start %d", d.operationCursor, starts[0])
	}
	d.Update(shiftTab) // -> back out to catalog
	if d.focus != runtimeFocusCatalog {
		t.Fatal("shift+tab past the first section should return to the catalog")
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
