package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestContextMeter_SavingsBadgeWhenCompacted(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), cumIn: 18000, ctxRaw: 340000, modelMaxTokens: 200000}
	out := stripAnsiCSI(m.renderContextMeter())
	if !strings.Contains(out, "↓") {
		t.Errorf("expected a savings badge when raw > sent:\n%s", out)
	}
}

func TestContextMeter_NoBadgeWhenNotCompacted(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), cumIn: 18000, ctxRaw: 18000, modelMaxTokens: 200000}
	out := stripAnsiCSI(m.renderContextMeter())
	if strings.Contains(out, "↓") {
		t.Errorf("no badge when sent==raw:\n%s", out)
	}
}

func TestContextMeter_CompactingOverlay(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), cumIn: 18000, ctxRaw: 340000, modelMaxTokens: 200000, compacting: true}
	out := stripAnsiCSI(m.renderContextMeter())
	if !strings.Contains(out, "compacting") {
		t.Errorf("expected a compacting overlay while a pass runs:\n%s", out)
	}
	// The "compacting…" label is overlaid on the 20-cell bar itself. With
	// fillN=1 and the label centered (start=4), col 0 stays a raw █ and
	// cols 1-3 + 15-19 stay raw ░ — so both glyphs remain visible alongside
	// the overlaid letters and the bar's fill ratio still reads through.
	if !strings.Contains(out, "█") {
		t.Errorf("expected the un-overlaid filled cell to remain visible:\n%s", out)
	}
	if !strings.Contains(out, "░") {
		t.Errorf("expected un-overlaid empty cells to remain visible:\n%s", out)
	}
	if !strings.Contains(out, "18.0k") || !strings.Contains(out, "200.0k") {
		t.Errorf("expected token count to remain visible during compaction:\n%s", out)
	}
	// The label appears exactly once — overlaid on the bar — and is not
	// also appended as a separate badge after the meter.
	if strings.Count(out, "compacting") != 1 {
		t.Errorf("expected exactly one compacting label (overlaid, not appended):\n%s", out)
	}
}
