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
	// The bar and token count must persist through a compaction pass — the
	// compacting label rides alongside the meter, not on top of it. This
	// guards against regressing back to the bar-replacement layout.
	if !strings.Contains(out, "█") {
		t.Errorf("expected the filled bar to remain visible during compaction:\n%s", out)
	}
	if !strings.Contains(out, "18.0k") || !strings.Contains(out, "200.0k") {
		t.Errorf("expected token count to remain visible during compaction:\n%s", out)
	}
}
