package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The tier picker is a floating modal composited over the dashboard,
// not a page replacement: both the picker rows and the underlying
// dashboard chrome must be visible in the same frame.
func TestTierPickerRendersAsModalOverDashboard(t *testing.T) {
	d := focusNavTestDashboard()
	d.height = 44 // tall enough that top-of-page chrome isn't under the centered box
	d.openTierPicker("most_capable.open")
	if d.tierPicker == nil {
		t.Fatal("picker did not open")
	}
	view := ansi.Strip(d.View())
	if !strings.Contains(view, "model tier") {
		t.Error("picker content missing from view")
	}
	if !strings.Contains(view, "download catalog") {
		t.Error("dashboard no longer visible under the picker — modal regressed to a page swap")
	}
}
