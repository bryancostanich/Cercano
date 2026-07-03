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
	mu       sync.Mutex
	turns    []conversation.Turn
	turnsErr error
	saved    *conversation.Compaction
	state    conversation.Compaction // returned by GetCompaction; updated by SaveCompaction
}

func (f *fakeStore) GetTurns(context.Context, string) ([]conversation.Turn, error) {
	return f.turns, f.turnsErr
}
func (f *fakeStore) GetCompaction(context.Context, string) (conversation.Compaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *fakeStore) SaveCompaction(_ context.Context, c conversation.Compaction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = &c
	f.state = c
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

func TestRunCompaction_LogsNoOpTerminalLine(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(3, 100)} // ~300 tokens, below floor → changed=false
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 100000, SegmentTokens: 8000, VerbatimRecent: 6}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	var buf strings.Builder
	g.logf = func(f string, a ...any) { fmt.Fprintf(&buf, f, a...) }

	if err := g.CompactNow(context.Background(), "conv-noop"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "pass start") {
		t.Errorf("expected 'pass start' in log; got:\n%s", out)
	}
	if !strings.Contains(out, "pass no-op") {
		t.Errorf("expected 'pass no-op' terminal line in log; got:\n%s", out)
	}
	if !strings.Contains(out, "conv-noop") {
		t.Errorf("expected conversation id in log; got:\n%s", out)
	}
}

func TestRunCompaction_LogsPrePassStoreFailure(t *testing.T) {
	fs := &fakeStore{turnsErr: fmt.Errorf("db locked")}
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)
	var buf strings.Builder
	g.logf = func(f string, a ...any) { fmt.Fprintf(&buf, f, a...) }

	err := g.CompactNow(context.Background(), "conv-prefail")
	if err == nil {
		t.Fatal("expected error from failing store, got nil")
	}
	out := buf.String()
	if !strings.Contains(out, "pass FAILED") {
		t.Errorf("expected 'pass FAILED' in log; got:\n%s", out)
	}
	if !strings.Contains(out, "conv-prefail") {
		t.Errorf("expected conversation id in log; got:\n%s", out)
	}
	if !strings.Contains(out, "db locked") {
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

func TestRegenerate_RebuildsFromRaw(t *testing.T) {
	fs := &fakeStore{
		turns: bigTurns(12, 1000),
		state: conversation.Compaction{
			ConversationID:   "c1",
			FrozenThrough:    9,
			ConsolidatedJSON: `{"goal":"poisoned summary from the default-temperature era"}`,
		},
	}
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{Goal: "fresh"}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)

	var lines []string
	pre, post, err := g.Regenerate(context.Background(), "c1", false, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if pre <= 0 || post <= 0 {
		t.Fatalf("want positive token counts, got pre=%d post=%d", pre, post)
	}
	if post >= pre {
		t.Fatalf("rebuild should compact: pre=%d post=%d", pre, post)
	}
	if len(lines) < 2 {
		t.Fatalf("want at least a start line and one pass line, got %v", lines)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.saved == nil {
		t.Fatal("expected rebuilt state to be persisted")
	}
	if strings.Contains(fs.saved.ConsolidatedJSON, "poisoned") {
		t.Fatalf("old state survived the rebuild: %s", fs.saved.ConsolidatedJSON)
	}
	if fs.saved.ConsolidatedJSON == "" || fs.saved.FrozenThrough == 0 {
		t.Errorf("rebuilt state incomplete: %+v", fs.saved)
	}
}

func TestRegenerate_SmallConversationClearsState(t *testing.T) {
	// Below the activation floor there is nothing to summarize — but regen
	// must still wipe the poisoned derived state so the context truly comes
	// from raw turns again.
	fs := &fakeStore{
		turns: bigTurns(3, 50),
		state: conversation.Compaction{ConversationID: "c1", ConsolidatedJSON: `{"goal":"poisoned"}`},
	}
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		t.Fatal("summarizer must not run below the activation floor")
		return compaction.StructuredSummary{}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 100000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)

	if _, _, err := g.Regenerate(context.Background(), "c1", false, nil); err != nil {
		t.Fatal(err)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.state.ConsolidatedJSON != "" || fs.state.FrozenThrough != 0 {
		t.Fatalf("derived state not cleared: %+v", fs.state)
	}
}

func TestRegenerate_RefusesWhilePassInFlight(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(3, 50)}
	g := New(fs, nil, compactor.Config{}, contextmeter.Default(), 10*time.Millisecond)

	if !g.claim("c1") {
		t.Fatal("claim should succeed on idle conversation")
	}
	defer g.release("c1")
	if _, _, err := g.Regenerate(context.Background(), "c1", false, nil); err == nil {
		t.Fatal("want error while another pass holds the claim")
	}
}

func TestScheduledPass_DefersWhileClaimHeld(t *testing.T) {
	// The normal compaction routine must not run while /context-regen holds
	// the conversation: a scheduled or hard-override pass defers (reschedules)
	// instead of interleaving Advance/Save with the rebuild.
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		t.Error("no pass may run while another holds the claim")
		return compaction.StructuredSummary{}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), time.Hour) // debounce far off: the deferred reschedule must not fire mid-test
	g.SetEnabled(true)

	if !g.claim("c1") {
		t.Fatal("claim should succeed on idle conversation")
	}
	defer g.release("c1")

	if err := g.CompactNow(context.Background(), "c1"); err != nil {
		t.Fatalf("deferred pass should be a clean no-op, got %v", err)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.saved != nil {
		t.Fatal("deferred pass must not persist state")
	}
}

func TestRegenerate_IncrementalKeepsExistingState(t *testing.T) {
	// /compact must digest only the backlog: existing consolidated state
	// survives, unlike the full rebuild which clears it first.
	fs := &fakeStore{
		turns: bigTurns(3, 50),
		state: conversation.Compaction{ConversationID: "c1", ConsolidatedJSON: `{"goal":"existing summary"}`},
	}
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		t.Fatal("summarizer must not run below the activation floor")
		return compaction.StructuredSummary{}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 100000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)

	if _, _, err := g.Regenerate(context.Background(), "c1", true, nil); err != nil {
		t.Fatal(err)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.state.ConsolidatedJSON != `{"goal":"existing summary"}` {
		t.Fatalf("incremental compaction must not clear existing state: %+v", fs.state)
	}
}
