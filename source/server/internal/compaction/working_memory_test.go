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

func TestWorkingMemoryRejectsRecordedFailures(t *testing.T) {
	for _, text := range []string{
		"GOAL: Summarize the conversation span for later reference\nFILES:\n- job.rs: [verified] source sha256:8fb51aa3495a29e16604f4fb940bc93c\nSTATE: Summary prepared.",
		"GOAL: Summarize the conversation span.\nSTATE: awaiting further instruction.",
		"GOAL: Implement aperture reuse\nFILES:\n- job.rs: [verified] read lines 1-560\nSTATE: Implementation pending",
	} {
		_, _, err := SummarizeBudgetedLocal(WithTaskReference(context.Background(), "Implement grading-only aperture reuse; preserve cancellation"), inspectedCode(), 32768, 1024, func(context.Context, string, int) (StructuredSummary, error) { return ParseSummary(text), nil })
		if !errors.Is(err, ErrUnhelpfulSummary) {
			t.Fatalf("accepted recorded failure: %v", err)
		}
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

func TestWorkingMemoryAllowsPlainConversationAndHistoricalFacts(t *testing.T) {
	sum := StructuredSummary{Goal: "Investigate cancellation", Findings: []string{"[superseded] The earlier JobKey omitted epoch; the later inspected version includes it."}, State: "Implementation pending"}
	if err := ValidateWorkingMemory(inspectedCode(), sum, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkingMemory([]llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Thanks, stop here."}}}}, StructuredSummary{State: "Awaiting further instructions"}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedSummaryBudgetIsBounded(t *testing.T) {
	calls := 0
	gate := NewSummaryGuard(2)
	bad := func(context.Context, []llm.Message) (StructuredSummary, error) {
		calls++
		return StructuredSummary{}, ErrUnhelpfulSummary
	}
	for i := 0; i < 5; i++ {
		_, err := gate.Summarize(context.Background(), inspectedCode(), bad)
		if !errors.Is(err, ErrUnhelpfulSummary) {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("provider calls=%d", calls)
	}
	gate.Reset()
	_, _ = gate.Summarize(context.Background(), inspectedCode(), bad)
	if calls != 3 {
		t.Fatal(calls)
	}
}
