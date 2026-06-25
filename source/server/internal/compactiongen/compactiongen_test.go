package compactiongen

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

type fakeStore struct {
	mu    sync.Mutex
	turns []conversation.Turn
	saved *conversation.Compaction
}

func (f *fakeStore) GetTurns(context.Context, string) ([]conversation.Turn, error) {
	return f.turns, nil
}
func (f *fakeStore) GetCompaction(context.Context, string) (conversation.Compaction, error) {
	return conversation.Compaction{}, nil
}
func (f *fakeStore) SaveCompaction(_ context.Context, c conversation.Compaction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = &c
	return nil
}

func bigTurns(n, tokensEach int) []conversation.Turn {
	body := strings.Repeat("lorem ipsum dolor sit amet ", tokensEach/5+1)
	var ts []conversation.Turn
	for i := 0; i < n; i++ {
		ts = append(ts, conversation.Turn{
			ID: fmt.Sprintf("t%d", i), Role: "user", Content: body,
			CreatedAt: time.Unix(int64(100+i), 0),
		})
	}
	return ts
}

func TestCompactNow_RunsAdvanceAndSaves(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{Goal: "g"}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)

	if err := g.CompactNow(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.saved == nil {
		t.Fatal("expected compaction state to be saved")
	}
	if fs.saved.ConsolidatedJSON == "" || fs.saved.FrozenThrough == 0 {
		t.Errorf("saved state incomplete: %+v", fs.saved)
	}
}

func TestCompactNow_SmallContextSavesNothing(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(3, 100)} // ~300 tokens, below floor
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 100000, SegmentTokens: 8000, VerbatimRecent: 6}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	if err := g.CompactNow(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if fs.saved != nil {
		t.Error("below activation floor should save nothing")
	}
}
