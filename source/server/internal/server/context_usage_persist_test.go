package server

import (
	"context"
	"testing"

	runnersvc "cercano/source/server/internal/runner"
)

// A served turn must leave a durable snapshot behind. The in-memory accounting
// map dies with the process, so without this the meter reports "unknown" after
// a restart — and shows 0 for a conversation loaded without running a turn.
func TestRecordRequestAccounting_PersistsDurableSnapshot(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}

	sink := &brokerSink{server: s, broker: s.turnBroker, conv: "c1"}
	sink.RecordRequestAccounting(runnersvc.RequestAccounting{
		MessageTokens:          38137,
		SystemTokens:           1178,
		ToolSchemaTokens:       12613,
		OutputReserveTokens:    8192,
		EstimatedRequestTokens: 60120,
		ContextWindow:          128000,
		ContextWindowKnown:     true,
	})

	got, ok, err := store.GetContextUsage(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a durable snapshot after a turn recorded its accounting")
	}
	if got.MessageTokens != 38137 || got.SystemTokens != 1178 ||
		got.ToolSchemaTokens != 12613 || got.OutputReserve != 8192 ||
		got.EstimatedRequest != 60120 {
		t.Errorf("snapshot lost accounting detail: %+v", got)
	}
	if got.ContextWindow != 128000 || !got.ContextWindowKnown {
		t.Errorf("snapshot lost window info: window=%d known=%v", got.ContextWindow, got.ContextWindowKnown)
	}
	if got.Source != "turn" {
		t.Errorf("snapshot source = %q, want \"turn\"", got.Source)
	}
	if got.ComputedAt.IsZero() {
		t.Error("snapshot must carry a computed-at timestamp")
	}
}

// An empty accounting record carries no information; caching it would let a
// meaningless zero overwrite a good snapshot.
func TestRecordRequestAccounting_IgnoresEmptyAccounting(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}

	sink := &brokerSink{server: s, broker: s.turnBroker, conv: "c1"}
	sink.RecordRequestAccounting(runnersvc.RequestAccounting{})

	if _, ok, err := store.GetContextUsage(ctx, "c1"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("empty accounting should not write a snapshot")
	}
}
