package builtins

import (
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/research"
)

func TestFormatDeepResearchProgressIncludesPhaseStepCounts(t *testing.T) {
	got := formatDeepResearchProgress(research.ProgressState{
		Phase:            "Analyzing",
		Step:             "Finding 3 of 12: ATIF v1.7 schema",
		Current:          2,
		Total:            12,
		FindingsAccepted: 5,
	})
	for _, want := range []string{"Analyzing 2/12", "Finding 3 of 12", "5 findings accepted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatDeepResearchProgress() = %q, missing %q", got, want)
		}
	}
}

func TestEmitDeepResearchActivityUsesStructuredChildProgress(t *testing.T) {
	var got agenttools.ProgressEvent
	call := &capabilities.Call{
		ConversationID: "parent-1",
		EmitProgress: func(ev agenttools.ProgressEvent) {
			got = ev
		},
	}

	emitDeepResearchActivity(call, "activity:deep_research:1", "started", "deep research start")

	if got.SubAgentID != "activity:deep_research:1" {
		t.Fatalf("SubAgentID = %q", got.SubAgentID)
	}
	if got.SubAgentParentID != "parent-1" {
		t.Fatalf("SubAgentParentID = %q", got.SubAgentParentID)
	}
	if got.SubAgentTitle != "research" {
		t.Fatalf("SubAgentTitle = %q", got.SubAgentTitle)
	}
	if got.Kind != "started" || got.Summary != "deep research start" || got.ToolName != "deep_research" {
		t.Fatalf("unexpected progress event: %+v", got)
	}
}
