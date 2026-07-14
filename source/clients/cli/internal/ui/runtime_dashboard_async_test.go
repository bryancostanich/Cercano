package ui

import (
	"strings"
	"testing"
)

// The runtime dashboard loads its snapshot off the UI thread (three
// sequential multi-second gRPC calls). Until the first snapshot arrives
// the page must render a lightweight placeholder rather than block the
// terminal or paint a misleading empty/error state.
func TestRuntimeDashboardRendersPlaceholderBeforeLoad(t *testing.T) {
	m := New(nil, false)
	d := &runtimeDashboard{
		width:   112,
		height:  37,
		palette: m.palette,
		styles:  m.styles,
		focus:   runtimeFocusCatalog,
		// loaded intentionally false: snapshot still in flight.
	}
	if d.loaded {
		t.Fatal("fresh dashboard should not be marked loaded")
	}
	if got := d.View(); !strings.Contains(got, "loading runtime") {
		t.Fatalf("pre-load view should show a loading placeholder, got:\n%s", got)
	}
}

// applySnapshot installs the async snapshot and flips loaded=true so the
// real content renders on the next paint.
func TestApplySnapshotMarksLoaded(t *testing.T) {
	m := New(nil, false)
	d := &runtimeDashboard{
		width:   112,
		height:  37,
		palette: m.palette,
		styles:  m.styles,
		mode:    dashboardModeRuntime,
		focus:   runtimeFocusCatalog,
	}
	d.applySnapshot(runtimeDashboardSnapshotMsg{
		snapshot: runtimeDashboardSnapshot{},
		mode:     dashboardModeRuntime,
	})
	if !d.loaded {
		t.Fatal("applySnapshot with matching mode should mark the dashboard loaded")
	}
}

// A snapshot whose fetch was kicked off before the dashboard was rebuilt
// in the other tab mode must be dropped — it would otherwise clobber the
// current tab's state with stale, mode-mismatched data.
func TestApplySnapshotDropsMismatchedMode(t *testing.T) {
	m := New(nil, false)
	d := &runtimeDashboard{
		width:   112,
		height:  37,
		palette: m.palette,
		styles:  m.styles,
		mode:    dashboardModeModels,
		focus:   runtimeFocusCatalog,
	}
	d.applySnapshot(runtimeDashboardSnapshotMsg{
		snapshot: runtimeDashboardSnapshot{},
		mode:     dashboardModeRuntime, // stale: different mode
	})
	if d.loaded {
		t.Fatal("mode-mismatched snapshot should be ignored, not applied")
	}
}
