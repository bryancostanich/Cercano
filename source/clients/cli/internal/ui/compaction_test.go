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
