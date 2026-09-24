package compactiongen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func memoryTurns() []conversation.Turn {
	ts := bigTurns(12, 1000)
	for i := range ts {
		ts[i].Role = "assistant"
	}
	ts[0].Role = "user"
	ts[0].Content = "Implement reuse; preserve cancellation"
	return ts
}
func TestTaskReferenceIgnoresToolWrappersAndPreambles(t *testing.T) {
	ts := memoryTurns()
	b, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "read", Content: "not a new task"}})
	ts = append(ts, conversation.Turn{Role: "user", BlocksJSON: string(b), CreatedAt: time.Unix(200, 0)}, conversation.Turn{Role: "user", Content: "[conversation summary]\nGoal: old", CreatedAt: time.Unix(201, 0)})
	if got := compaction.LatestTaskReference(agent.BuildLLMHistory(ts)); got != ts[0].Content {
		t.Fatal(got)
	}
}
func TestBackgroundRejectionBudgetAndTaskChange(t *testing.T) {
	store := &fakeStore{turns: memoryTurns()}
	original := append([]conversation.Turn(nil), store.turns...)
	calls := 0
	summarizer := func(ctx context.Context, _ []llm.Message) (compaction.StructuredSummary, error) {
		calls++
		if got := compaction.TaskReferenceFrom(ctx); got != store.turns[0].Content {
			t.Fatalf("task=%q", got)
		}
		return compaction.StructuredSummary{}, compaction.ErrUnhelpfulSummary
	}
	g := New(store, summarizer, compactor.Config{ActivationFloorTokens: 100, SegmentTokens: 4000, VerbatimRecent: 2}, contextmeter.Default(), time.Hour)
	defer g.Close(context.Background())
	g.SetEnabled(true)
	g.logf = func(string, ...any) {}
	for i := 0; i < 4; i++ {
		err := g.runCompaction(context.Background(), "c")
		if !errors.Is(err, compaction.ErrUnhelpfulSummary) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if calls != 2 || store.saved != nil || !reflect.DeepEqual(original, store.turns) {
		t.Fatalf("calls=%d saved=%v raw changed=%v", calls, store.saved, !reflect.DeepEqual(original, store.turns))
	}
	store.turns[0].Content = "Investigate a new task"
	_ = g.runCompaction(context.Background(), "c")
	if calls != 3 {
		t.Fatal(calls)
	}
	// Explicit regeneration gets a fresh budget even when the background budget
	// for this task is exhausted. Rejection still must not save a new summary.
	_ = g.runCompaction(context.Background(), "c")
	if calls != 4 {
		t.Fatal(calls)
	}
	_, _, err := g.Regenerate(context.Background(), "c", true, nil)
	if !errors.Is(err, compaction.ErrUnhelpfulSummary) || calls != 5 || store.saved != nil {
		t.Fatalf("calls=%d saved=%v err=%v", calls, store.saved, err)
	}
}
func TestBackgroundReferenceUsesSameSnapshotAndGuardIsBounded(t *testing.T) {
	store := &fakeStore{turns: memoryTurns()}
	calls := 0
	g := New(store, func(ctx context.Context, _ []llm.Message) (compaction.StructuredSummary, error) {
		calls++
		if compaction.TaskReferenceFrom(ctx) != store.turns[0].Content {
			t.Fatal("task missing")
		}
		return compaction.StructuredSummary{Goal: "Implement reuse", Findings: []string{"JobKey contains epoch and grading identity."}}, nil
	}, compactor.Config{ActivationFloorTokens: 100, SegmentTokens: 4000, VerbatimRecent: 2}, contextmeter.Default(), time.Hour)
	defer g.Close(context.Background())
	g.SetEnabled(true)
	g.logf = func(string, ...any) {}
	if err := g.runCompaction(context.Background(), "c"); err != nil || calls == 0 || store.saved == nil {
		t.Fatalf("calls=%d saved=%v err=%v", calls, store.saved, err)
	}
	first := g.getGuard("same", "old")
	second := g.getGuard("same", "new")
	if first == second {
		t.Fatal("new task reused failed guard")
	}
	for i := 0; i < 200; i++ {
		g.getGuard(fmt.Sprint(i), "task")
	}
	if len(g.guards) > 128 || len(g.guardKeys) > 128 {
		t.Fatal("unbounded rejection state")
	}
}
