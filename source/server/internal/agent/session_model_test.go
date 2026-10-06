package agent

import (
	"cercano/source/server/internal/llm"
	"testing"
)

func TestSessionModelAlwaysConfirmsChangesButNotReads(t *testing.T) {
	for _, mode := range []PermissionMode{ModeStrict, ModePermissive, ModeBypass} {
		if !GateDecisionForTool(mode, llm.PermX, "session_model", false, false) {
			t.Fatalf("%s skipped model change confirmation", mode)
		}
		if GateDecisionForTool(mode, llm.PermR, "session_model", false, false) {
			t.Fatalf("%s prompted for model status", mode)
		}
	}
	if !PlanProfile().Allows(llm.PermX, "session_model") {
		t.Fatal("planning fence prevents session control")
	}
}
