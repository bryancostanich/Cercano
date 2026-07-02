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

func TestRunCompaction_LogsPassOk(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{Goal: "g"}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	var buf strings.Builder
	g.logf = func(f string, a ...any) { fmt.Fprintf(&buf, f, a...) }

	if err := g.CompactNow(context.Background(), "conv-ok"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "pass ok") {
		t.Errorf("expected 'pass ok' in log; got:\n%s", out)
	}
	if !strings.Contains(out, "conv-ok") {
		t.Errorf("expected conversation id in log; got:\n%s", out)
	}
	if !strings.Contains(out, "tokens") {
		t.Errorf("expected token counts in log; got:\n%s", out)
	}
	if !strings.Contains(out, "more=") {
		t.Errorf("expected more flag in log; got:\n%s", out)
	}
}

func TestRunCompaction_LogsPassFailed(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{}, fmt.Errorf("summarize error")
	}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	var buf strings.Builder
	g.logf = func(f string, a ...any) { fmt.Fprintf(&buf, f, a...) }

	err := g.CompactNow(context.Background(), "conv-fail")
	if err == nil {
		t.Fatal("expected error from failed summarize, got nil")
	}
	out := buf.String()
	if !strings.Contains(out, "pass FAILED") {
		t.Errorf("expected 'pass FAILED' in log; got:\n%s", out)
	}
	if !strings.Contains(out, "conv-fail") {
		t.Errorf("expected conversation id in log; got:\n%s", out)
	}
	if !strings.Contains(out, "summarize error") {
		t.Errorf("expected error text in log; got:\n%s", out)
	}
}

func TestIsCompacting_TrueDuringPass(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	release := make(chan struct{})
	entered := make(chan struct{})
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // block so the pass stays in-flight
		return compaction.StructuredSummary{Goal: "g"}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)

	go func() { _ = g.CompactNow(context.Background(), "c1") }()
	<-entered
	if !g.IsCompacting("c1") {
		t.Error("IsCompacting should be true while a pass runs")
	}
	close(release)
	// Wait for the pass to finish, then it must be false.
	deadline := time.Now().Add(2 * time.Second)
	for g.IsCompacting("c1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if g.IsCompacting("c1") {
		t.Error("IsCompacting should clear after the pass finishes")
	}
}
