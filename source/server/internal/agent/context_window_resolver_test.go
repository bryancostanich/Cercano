package agent

import (
	"testing"

	"cercano/source/server/internal/contextmeter"
)

// The live meter denominator must track the capacity the turn actually used,
// or the UI reports a percentage of a window that was never applied.
func TestRecordContextUsage_UsesResolvedWindow(t *testing.T) {
	a := &Agent{meter: contextmeter.NewRegistry()}
	a.SetContextWindowResolver(func(model string) (int, bool) {
		if model == "hosted/big" {
			return 262_144, true
		}
		return 0, false
	})
	a.RecordContextUsage("conv", "hosted/big", 10, 5)
	_, max := a.GetContextUsage(nil, "conv")
	if max != 262_144 {
		t.Fatalf("meter max = %d, want 262144", max)
	}
}

// Absent evidence keeps the conventional per-family denominator.
func TestRecordContextUsage_FallsBackWhenUnknown(t *testing.T) {
	a := &Agent{meter: contextmeter.NewRegistry()}
	a.SetContextWindowResolver(func(string) (int, bool) { return 0, false })
	a.RecordContextUsage("conv", "claude-sonnet-4-6", 10, 5)
	_, max := a.GetContextUsage(nil, "conv")
	if max != contextmeter.ModelMax("claude-sonnet-4-6") {
		t.Fatalf("meter max = %d, want conventional window", max)
	}
}

// No resolver at all behaves exactly as before the change.
func TestRecordContextUsage_NilResolverKeepsPreviousBehavior(t *testing.T) {
	a := &Agent{meter: contextmeter.NewRegistry()}
	a.RecordContextUsage("conv", "claude-sonnet-4-6", 10, 5)
	_, max := a.GetContextUsage(nil, "conv")
	if max != contextmeter.ModelMax("claude-sonnet-4-6") {
		t.Fatalf("meter max = %d, want conventional window", max)
	}
}
