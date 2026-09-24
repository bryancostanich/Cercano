package compaction

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// Real trailing user messages from main conversations. None of these is an
// assigned task, and none may be presented to the summarizer as one.
var realMainThreadMessages = []string{
	"push",
	"continue",
	"land on main",
	"middle button drag left/right is backwards. up/down is correct",
	"ok, let's try it",
}

func TestMainThreadMessageNeverBecomesAnAssignedTask(t *testing.T) {
	for _, msg := range realMainThreadMessages {
		ctx := WithUserIntentHint(context.Background(), msg)
		if got := TaskReferenceFrom(ctx); got != "" {
			t.Fatalf("intent hint leaked as assigned task: %q", got)
		}
		prompt := BuildSummaryPromptWithTask(codeSpan(), TaskReferenceFrom(ctx))
		if strings.Contains(prompt, "task reference") {
			t.Fatalf("main-thread prompt invented a task section for %q", msg)
		}
		if strings.Contains(prompt, msg) && msg != "continue" {
			t.Fatalf("main-thread prompt embedded %q as task context", msg)
		}
	}
}

// A dispatch has a genuine assigned task, so it still reaches the prompt.
func TestAssignedTaskStillReachesPrompt(t *testing.T) {
	task := "Implement grading-only aperture reuse; preserve cancellation"
	ctx := WithTaskReference(context.Background(), task)
	prompt := BuildSummaryPromptWithTask(codeSpan(), TaskReferenceFrom(ctx))
	if !strings.Contains(prompt, task) || !strings.Contains(prompt, "read-only task reference") {
		t.Fatal("assigned task no longer supplied to the summarizer")
	}
}

// The gate's narrow exemptions may still consult the latest user message, since
// that never reaches the model. A user winding down must not force a rejection.
func TestIntentHintStillInformsGateExemptions(t *testing.T) {
	// Substantive findings, but a wind-down state: only the exemption differs.
	idle := StructuredSummary{
		Goal:     "Investigate cancellation",
		Findings: []string{"job.rs: SiteMeshJobKey carries grading identity; non-grading inputs must invalidate reuse."},
		State:    "Awaiting further instructions",
	}
	if err := ValidateWorkingMemory(codeSpan(), idle, GateIntentFrom(WithUserIntentHint(context.Background(), "thanks, stop here"))); err != nil {
		t.Fatalf("user-closed exemption lost: %v", err)
	}
	if err := ValidateWorkingMemory(codeSpan(), idle, GateIntentFrom(WithUserIntentHint(context.Background(), "push"))); err == nil {
		t.Fatal("idle claim accepted mid-investigation")
	}
}

func codeSpan() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "r", ToolName: "Read", ToolInput: []byte(`{"path":"job.rs"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "r", Content: strings.Repeat("pub struct SiteMeshJobKey { grading: GradingSnapshot }\npub fn build_site_mesh_for_key() {}\n", 6)}}},
	}
}
