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
)

type usageRecorder struct {
	mu    sync.Mutex
	convs []string
	sizes []int
	raws  []int
}

func (r *usageRecorder) fn(_ context.Context, conversationID string, sentTokens, rawTokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.convs = append(r.convs, conversationID)
	r.sizes = append(r.sizes, sentTokens)
	r.raws = append(r.raws, rawTokens)
}

func (r *usageRecorder) last() (string, int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sizes) == 0 {
		return "", 0, false
	}
	return r.convs[len(r.convs)-1], r.sizes[len(r.sizes)-1], true
}

func (r *usageRecorder) lastRaw() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.raws) == 0 {
		return 0
	}
	return r.raws[len(r.raws)-1]
}

func (r *usageRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sizes)
}

func okSummarize(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
	return compaction.StructuredSummary{Goal: "g"}, nil
}

// A completed pass must cache the post-pass send-view total it already
// computed, so the meter never reassembles the conversation to answer a poll.
func TestCompactNow_PersistsContextUsageSnapshot(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, okSummarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	rec := &usageRecorder{}
	g.SetContextUsageFn(rec.fn)

	if err := g.CompactNow(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}

	conv, sent, ok := rec.last()
	if !ok {
		t.Fatal("expected a context-usage snapshot after a successful pass")
	}
	if conv != "c1" {
		t.Errorf("snapshot recorded for wrong conversation: %q", conv)
	}
	if sent <= 0 {
		t.Errorf("snapshot must carry a positive send-view total, got %d", sent)
	}

	// The cached value must equal the pass's own post-pass view total.
	fs.mu.Lock()
	state := fs.state
	fs.mu.Unlock()
	view, err := compactor.BuildSendView(fs.turns, state)
	if err != nil {
		t.Fatal(err)
	}
	if want := compaction.TotalTokens(contextmeter.Default(), view); sent != want {
		t.Errorf("snapshot total %d != post-pass view total %d", sent, want)
	}

	// Raw size is measured from turns the pass already loaded, and a compacted
	// send view must be smaller than the raw backlog it summarizes.
	raw := rec.lastRaw()
	if raw <= 0 {
		t.Errorf("expected a positive raw-token measurement, got %d", raw)
	}
	if raw <= sent {
		t.Errorf("raw tokens (%d) should exceed the compacted send view (%d)", raw, sent)
	}
}

// A no-op pass changes nothing, so it must not write a snapshot.
func TestCompactNow_NoOpPassWritesNoSnapshot(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(3, 100)} // below the activation floor
	cfg := compactor.Config{ActivationFloorTokens: 100000, SegmentTokens: 8000, VerbatimRecent: 6}
	g := New(fs, okSummarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	rec := &usageRecorder{}
	g.SetContextUsageFn(rec.fn)

	if err := g.CompactNow(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if n := rec.count(); n != 0 {
		t.Errorf("no-op pass should not write a snapshot, got %d writes", n)
	}
}

// Clearing rehydrates the raw backlog, raising the send-view size. The cache
// must be updated so the meter stops serving the smaller pre-clear snapshot.
func TestClear_PersistsPostClearSnapshot(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, okSummarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	rec := &usageRecorder{}
	g.SetContextUsageFn(rec.fn)

	if err := g.CompactNow(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	_, compacted, ok := rec.last()
	if !ok {
		t.Fatal("expected a snapshot from the compaction pass")
	}

	if _, postTokens, err := g.Clear(context.Background(), "c1", nil); err != nil {
		t.Fatal(err)
	} else if postTokens <= 0 {
		t.Fatalf("expected positive post-clear tokens, got %d", postTokens)
	}

	_, afterClear, ok := rec.last()
	if !ok {
		t.Fatal("expected a snapshot after Clear")
	}
	if afterClear <= compacted {
		t.Errorf("post-clear snapshot (%d) should exceed the compacted snapshot (%d)", afterClear, compacted)
	}
}

// Regenerate persists its final state, so the cache must reflect it.
func TestRegenerate_PersistsFinalSnapshot(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, okSummarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	rec := &usageRecorder{}
	g.SetContextUsageFn(rec.fn)

	_, postTokens, err := g.Regenerate(context.Background(), "c1", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	conv, sent, ok := rec.last()
	if !ok {
		t.Fatal("expected a snapshot after Regenerate")
	}
	if conv != "c1" || sent != postTokens {
		t.Errorf("snapshot %q/%d should match Regenerate's final total %d", conv, sent, postTokens)
	}
}

// The snapshot is a derived cache: with no writer wired, passes must behave
// exactly as before.
func TestCompactNow_NilUsageFnIsSafe(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, okSummarize, cfg, contextmeter.Default(), 10*time.Millisecond)

	if err := g.CompactNow(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.saved == nil {
		t.Fatal("pass should still save compaction state with no usage writer")
	}
}
