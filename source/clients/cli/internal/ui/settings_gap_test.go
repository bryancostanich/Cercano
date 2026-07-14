package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
)

// TestConfigSurfaceNoGapAboveStatusBar guards the reported regression: with a
// content page (the settings surface) active, the page body plus the actual
// surrounding chrome (header, divider, tab strip, status bar) must fill the
// terminal exactly — no blank gap floated between the body and the status bar.
//
// The bug was dashboardContentHeight reserving rows for a prompt frame that
// content pages never render, so the scroll region came up short and View()
// padded the difference with blank lines above the status bar.
func TestConfigSurfaceNoGapAboveStatusBar(t *testing.T) {
	m := New(nil, false)
	m.splashShown = false // no splash: keep the frame math simple and deterministic

	// Open the config surface on a settings tab, then size the terminal.
	m.openConfigSurface(configTabUI)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 35})
	m = nm.(Model)

	view := ansi.Strip(m.View().Content)
	rows := strings.Split(view, "\n")

	// Locate the status bar (it always carries the "/help for cmds" hint).
	statusRow := -1
	for i, r := range rows {
		if strings.Contains(r, "/help for cmds") {
			statusRow = i
			break
		}
	}
	if statusRow < 0 {
		t.Fatalf("status bar not found in frame:\n%s", view)
	}

	// Find the last non-blank row strictly above the status bar. There must be
	// no gap: the content body should butt directly against the status bar
	// (allowing at most the page's own trailing rule, never a run of blanks).
	lastContent := -1
	for i := statusRow - 1; i >= 0; i-- {
		if strings.TrimSpace(rows[i]) != "" {
			lastContent = i
			break
		}
	}
	if lastContent < 0 {
		t.Fatalf("no content rows above status bar:\n%s", view)
	}
	gap := statusRow - lastContent - 1
	if gap > 0 {
		t.Fatalf("found %d blank row(s) between content (row %d) and status bar (row %d):\n%s",
			gap, lastContent, statusRow, view)
	}
}
