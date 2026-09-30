package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func inspectedCode() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "read-code", ToolName: "Read", ToolInput: []byte(`{"path":"job.rs"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "read-code", Content: strings.Repeat("pub struct JobKey { grading: Snapshot, shape: Shape, epoch: Epoch }\n", 10)}}},
	}
}

func TestWorkingMemoryTaskReferenceAndFindings(t *testing.T) {
	task := "Implement aperture reuse. Do not mutate published meshes."
	_, _, err := SummarizeBudgetedLocal(WithTaskReference(context.Background(), task), inspectedCode(), 32768, 1024, func(_ context.Context, prompt string, _ int) (StructuredSummary, error) {
		if !strings.Contains(prompt, task) || !strings.Contains(prompt, "read-only task reference") || !strings.Contains(prompt, "FINDINGS:") {
			t.Fatalf("missing working-memory contract")
		}
		return StructuredSummary{Goal: "Implement aperture reuse", Findings: []string{"job.rs: JobKey includes grading, shape and epoch; preserve non-grading invalidation."}, State: "Implementation pending"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkingMemoryReferenceCountsTowardBudget(t *testing.T) {
	called := false
	_, _, err := SummarizeBudgetedLocal(WithTaskReference(context.Background(), strings.Repeat("large task constraints ", 5000)), inspectedCode(), 4096, 1024, func(context.Context, string, int) (StructuredSummary, error) {
		called = true
		return StructuredSummary{}, nil
	})
	var deferred *DeferralError
	if !errors.As(err, &deferred) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
