package compactiongen

import (
	"context"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

func TestBackgroundCompactionCarriesAccounting(t *testing.T) {
	store := &fakeStore{turns: bigTurns(12, 1000)}
	observed := make(chan usage.AttemptObservation, 1)
	var once sync.Once
	g := New(store, func(ctx context.Context, _ []llm.Message) (compaction.StructuredSummary, error) {
		usage.StartAttempt(ctx, "fake", "fake").Finish(usage.Completed)
		return compaction.StructuredSummary{Goal: "goal"}, nil
	}, compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}, contextmeter.Default(), time.Millisecond)
	g.SetAttemptSink(func(a usage.AttemptObservation) bool { once.Do(func() { observed <- a }); return true })
	g.SetEnabled(true)
	g.Schedule("conversation")
	select {
	case a := <-observed:
		if a.Attribution.Source != "compaction" || a.Attribution.ConversationID != "conversation" || a.Attribution.OperationID == "" {
			t.Fatalf("background attribution=%+v", a.Attribution)
		}
	case <-time.After(time.Second):
		t.Fatal("background compaction accounting missing")
	}
}
