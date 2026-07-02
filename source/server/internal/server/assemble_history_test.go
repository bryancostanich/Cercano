package server

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/config"
)

// schedSpy is a CompactionScheduler stub that records Schedule calls.
type schedSpy struct {
	ids []string
}

func (s *schedSpy) Schedule(id string)                           { s.ids = append(s.ids, id) }
func (s *schedSpy) CompactNow(_ context.Context, _ string) error { return nil }
func (s *schedSpy) IsCompacting(_ string) bool                   { return false }

// TestAssembleHistory_HardOverrideNonBlocking verifies that when the assembled
// view exceeds the hard-override limit, assembleHistory:
//   - calls ScheduleCompaction (no inline model call),
//   - returns a view that fits under the limit via LLM-free front-drop only.
//
// Shape used: concrete agent.Agent with fakeCompactionScheduler spy via
// agent.WithCompactionScheduler; no CompactNow call is possible (spy would
// return nil harmlessly, but the reworked code never calls it).
func TestAssembleHistory_HardOverrideNonBlocking(t *testing.T) {
	ctx := context.Background()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	spy := &schedSpy{}
	a := agent.NewAgent(&mockRouter{}, &mockCoordinator{},
		agent.WithPersistentStore(store),
		agent.WithCompactionScheduler(spy),
	)

	srv := NewServer(a, nil, nil, nil, nil, nil)
	// HardOverridePct = 0.001 → hardLimit = int(0.001 * 128000) = 128 tokens for
	// the default "test-model" (no specific match → ModelMax returns 128 000).
	srv.SetConfigPersistence("", config.Config{
		CloudModel: "test-model",
		Compaction: config.CompactionConfig{
			Enabled:         true,
			HardOverridePct: 0.001,
		},
	})

	convID := "test-assemble-hard-override"
	_ = store.EnsureConversation(ctx, convID, "/p", "test-model")

	// Seed 4 messages, each with enough text to push total well over 128 tokens.
	// strings.Repeat("hello ", 50) ≈ 50 tokens (fallback char/4 or tiktoken).
	// Four such messages ≈ 200-300 tokens > 128.
	big := strings.Repeat("hello ", 50)
	roles := []string{"user", "assistant", "user", "assistant"}
	for _, role := range roles {
		_ = store.Append(ctx, conversation.Turn{
			ConversationID: convID,
			Role:           role,
			Content:        big,
		})
	}

	view := srv.assembleHistory(ctx, store, convID)

	// ScheduleCompaction must have been called — the reworked path kicks the
	// background generator instead of blocking inline.
	if len(spy.ids) == 0 {
		t.Error("ScheduleCompaction was not called; expected background kick")
	}

	// The view must be non-nil and fit under the hard limit.
	// TruncateOldestToFit keeps at least 1 message; one message (~50 tokens) < 128.
	hardLimit := int(float64(contextmeter.ModelMax("test-model")) * 0.001)
	if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
		t.Errorf("view still over hard limit: tokens=%d limit=%d",
			compaction.TotalTokens(contextmeter.Default(), view), hardLimit)
	}

	// The view is strictly shorter than the seeded history (messages were dropped).
	if len(view) >= len(roles) {
		t.Errorf("expected messages to be dropped; got len(view)=%d >= seeded=%d",
			len(view), len(roles))
	}
}
