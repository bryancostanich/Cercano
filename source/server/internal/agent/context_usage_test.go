package agent

import (
	"context"
	"testing"

	"cercano/source/server/internal/contextmeter"
)

func TestRecordContextUsage_SnapshotsAgainstModel(t *testing.T) {
	reg := contextmeter.NewRegistry()
	a := &Agent{}
	WithContextMeter(reg, "qwen3-coder")(a) // some local default; the call passes a cloud model explicitly

	a.RecordContextUsage("c1", "claude-opus-4", 1000, 200)

	used, max := a.GetContextUsage(context.Background(), "c1")
	if used != 1200 {
		t.Errorf("used = %d, want 1200 (in+out)", used)
	}
	if want := contextmeter.ModelMax("claude-opus-4"); max != want {
		t.Errorf("max = %d, want %d (cloud model window)", max, want)
	}

	// A zero-input update must not clobber the prior reading.
	a.RecordContextUsage("c1", "claude-opus-4", 0, 0)
	used2, _ := a.GetContextUsage(context.Background(), "c1")
	if used2 != 1200 {
		t.Errorf("used after zero update = %d, want 1200 (unchanged)", used2)
	}
}
