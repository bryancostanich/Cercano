package ui

import (
	"errors"
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// The reported bug: loading a large conversation showed a context of 0. A
// failed poll means "unknown right now", so the last known reading must
// survive instead of being cleared to zero.
func TestCtxUsage_PollErrorRetainsLastKnownValues(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), convID: "c1"}

	m2, _ := m.Update(ctxUsageMsg{
		Used:                   52_000,
		Max:                    128_000,
		Raw:                    9_400_000,
		MessageTokens:          44_000,
		SystemTokens:           1_200,
		ToolSchemaTokens:       8_000,
		OutputReserveTokens:    8_192,
		EstimatedRequestTokens: 61_392,
		UsageSource:            "snapshot",
	})
	mm := m2.(Model)
	if mm.ctxEstimatedRequest != 61_392 || mm.ctxRaw != 9_400_000 {
		t.Fatalf("precondition failed: est=%d raw=%d", mm.ctxEstimatedRequest, mm.ctxRaw)
	}

	m3, _ := mm.Update(ctxUsageMsg{Err: errors.New("context deadline exceeded")})
	got := m3.(Model)

	if got.ctxEstimatedRequest != 61_392 {
		t.Errorf("estimated request cleared on poll error: %d, want 61392 retained", got.ctxEstimatedRequest)
	}
	if got.ctxRaw != 9_400_000 {
		t.Errorf("raw cleared on poll error: %d, want 9400000 retained", got.ctxRaw)
	}
	if got.ctxMessageTokens != 44_000 || got.ctxSystemTokens != 1_200 ||
		got.ctxToolSchemaTokens != 8_000 || got.ctxOutputReserveTokens != 8_192 {
		t.Errorf("accounting detail cleared on poll error: %+v", got)
	}
	if !got.ctxUsageStale {
		t.Error("a failed poll should mark the retained reading stale")
	}
	if got.compacting {
		t.Error("compacting should be cleared when the poll fails")
	}
}

func TestCtxUsage_SuccessfulPollCarriesProvenance(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), convID: "c1"}
	m2, _ := m.Update(ctxUsageMsg{
		Used:                   1_000,
		Max:                    128_000,
		EstimatedRequestTokens: 1_000,
		UsageSource:            "raw_estimate",
		UsageStale:             true,
	})
	got := m2.(Model)
	if got.ctxUsageSource != "raw_estimate" {
		t.Errorf("ctxUsageSource = %q, want \"raw_estimate\"", got.ctxUsageSource)
	}
	if !got.ctxUsageStale {
		t.Error("expected stale flag to be carried from the agent response")
	}
}

// An unknown reading must never render as a 0 / window meter, which reads as
// "this conversation has no context".
func TestContextView_UnknownUsageDoesNotRenderZeroMeter(t *testing.T) {
	c := &contextView{
		width:  100,
		styles: theme.NewStyles(theme.Cracker()),
		snapshot: contextSnapshot{
			Usage: &agentclient.ContextUsage{
				ModelMax:    128_000,
				UsageSource: "none",
			},
		},
	}
	head := c.renderHeader()
	if !strings.Contains(head, "not computed yet") {
		t.Errorf("unknown usage should say so, got %q", head)
	}
	if strings.Contains(head, "0 / 128,000") {
		t.Errorf("unknown usage must not render a zero meter, got %q", head)
	}
}

func TestContextView_ApproximateUsageIsMarked(t *testing.T) {
	c := &contextView{
		width:  100,
		styles: theme.NewStyles(theme.Cracker()),
		snapshot: contextSnapshot{
			Usage: &agentclient.ContextUsage{
				TokensUsed:             52_000,
				EstimatedRequestTokens: 52_000,
				MessageTokens:          52_000,
				ModelMax:               128_000,
				ContextWindowKnown:     true,
				UsageSource:            "raw_estimate",
			},
		},
	}
	head := c.renderHeader()
	if !strings.Contains(head, "approximate") {
		t.Errorf("a raw estimate should be marked approximate, got %q", head)
	}
}
