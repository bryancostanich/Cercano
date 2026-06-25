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
}
