package ui

import (
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestCtxUsageMsg_StoresRawAndCompacting(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), convID: "c1"}
	m2, _ := m.Update(ctxUsageMsg{Used: 18000, Max: 200000, Raw: 340000, Compacting: true})
	mm := m2.(Model)
	if mm.cumIn != 18000 || mm.ctxRaw != 340000 || !mm.compacting {
		t.Errorf("ctxUsageMsg not stored: cumIn=%d ctxRaw=%d compacting=%v", mm.cumIn, mm.ctxRaw, mm.compacting)
	}
}

func TestCtxUsageTick_DecrementsAndSettles(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), convID: "c1", ctxPollTicks: 2, ctxPolling: true}
	// tick 1: warm window 2 -> 1, loop keeps running.
	m1, _ := m.Update(ctxUsageTickMsg{})
	m = m1.(Model)
	if m.ctxPollTicks != 1 || !m.ctxPolling {
		t.Fatalf("after tick 1: ticks=%d polling=%v, want 1/true", m.ctxPollTicks, m.ctxPolling)
	}
	// tick 2: 1 -> 0, loop settles and marks itself idle so the next turn restarts it.
	m2, _ := m.Update(ctxUsageTickMsg{})
	m = m2.(Model)
	if m.ctxPollTicks != 0 || m.ctxPolling {
		t.Errorf("after tick 2 (settle): ticks=%d polling=%v, want 0/false", m.ctxPollTicks, m.ctxPolling)
	}
}

func TestCtxUsageTick_StaysWhileCompacting(t *testing.T) {
	// Warm window exhausted, but a compaction pass is in flight → keep polling
	// so the footer observes the flag clearing.
	m := Model{styles: theme.NewStyles(theme.Cracker()), convID: "c1", ctxPollTicks: 0, ctxPolling: true, compacting: true}
	m1, _ := m.Update(ctxUsageTickMsg{})
	m = m1.(Model)
	if !m.ctxPolling {
		t.Error("while compacting the loop must keep running (ctxPolling stays true)")
	}
}
