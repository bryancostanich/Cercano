package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"cercano/source/server/pkg/agentclient"
)

// newActionCursorTestDashboard builds a dashboard whose rendered page
// contains actionable rows in TWO different action blocks: installed
// models (start/delete for a downloaded model) and model tiers (every
// slot is an enter-to-change row).
func newActionCursorTestDashboard() *runtimeDashboard {
	m := New(nil, false)
	return &runtimeDashboard{
		width:   112,
		height:  40,
		palette: m.palette,
		styles:  m.styles,
		focus:   runtimeFocusActions,
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{},
			Status: &agentclient.RuntimeStatus{
				Models: []agentclient.RuntimeModel{{
					ID:            "llama_server:catalog:test-model",
					DisplayName:   "Test Model",
					Runtime:       "llama_server",
					DownloadState: "downloaded",
					Path:          "/tmp/test-model.gguf",
				}},
			},
		},
	}
}

// countSelectionMarkers counts action-row cursor markers in a render.
func countSelectionMarkers(s string) int {
	return strings.Count(ansi.Strip(s), "▶")
}

// Regression test: the action blocks share one flat cursor space, but
// renderActionBlock used to restart its ordinal at 0 per block — so
// the Nth actionable row of EVERY block rendered as selected at once
// (installed models and model tiers moved their highlights together).
func TestActionCursor_ExactlyOneSelectionAcrossBlocks(t *testing.T) {
	d := newActionCursorTestDashboard()
	total := len(d.operationRows())
	actionable := 0
	for _, row := range d.operationRows() {
		if row.Action.Kind != "" {
			actionable++
		}
	}
	if actionable < 4 {
		t.Fatalf("test setup needs actionable rows in two blocks, got %d (rows %d)", actionable, total)
	}
	for cursor := 0; cursor < actionable; cursor++ {
		d.operationCursor = cursor
		full, _ := d.fullContent()
		if got := countSelectionMarkers(full); got != 1 {
			t.Fatalf("cursor=%d rendered %d selection markers, want exactly 1", cursor, got)
		}
	}
}

// The marker must move to a DIFFERENT line when the cursor advances —
// guards against the ordinal getting stuck as well as double-render.
func TestActionCursor_MarkerMovesWithCursor(t *testing.T) {
	d := newActionCursorTestDashboard()
	markerLine := func() string {
		full, _ := d.fullContent()
		for _, line := range strings.Split(ansi.Strip(full), "\n") {
			if strings.Contains(line, "▶") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	d.operationCursor = 0
	first := markerLine()
	d.operationCursor = 1
	second := markerLine()
	if first == "" || second == "" {
		t.Fatalf("marker missing (first=%q second=%q)", first, second)
	}
	if first == second {
		t.Fatalf("marker did not move: %q", first)
	}
}
