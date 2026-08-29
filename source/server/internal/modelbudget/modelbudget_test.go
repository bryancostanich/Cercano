package modelbudget

import "testing"

func TestForTargetComputesInputBudget(t *testing.T) {
	budget, err := ForTarget(Target{Provider: "llama_server", Model: "tiny", ContextWindow: 8192, ContextWindowKnown: true}, 2048, 512)
	if err != nil {
		t.Fatal(err)
	}
	if budget.InputTokens != 8192-2048-512 {
		t.Fatalf("InputTokens = %d, want %d", budget.InputTokens, 8192-2048-512)
	}
}

func TestForTargetRequiresKnownWindow(t *testing.T) {
	_, err := ForTarget(Target{Provider: "llama_server", Model: "unknown"}, 2048, 512)
	if err == nil {
		t.Fatal("expected unknown-window error")
	}
}
