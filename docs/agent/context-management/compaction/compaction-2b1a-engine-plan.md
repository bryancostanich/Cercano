# Compaction 2b-1a — Engine + Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the deterministic, stateful compaction engine — a `Reduce` helper, the persisted derived layer (`conversation_compaction`), and the pure `Advance`/`BuildSendView` functions that freeze segments and assemble the sent history — all testable with a fake summarizer, no live model, no request-path change yet.

**Architecture:** A new `source/server/internal/compactor` package orchestrates over conversation turns + persisted compaction state, reusing the part-1/2 `compaction` primitives. `Advance` runs one stateful compaction pass (gates → freeze new segments → reduce); `BuildSendView` assembles the consolidated summary + live tail for the request path. The `conversation` store gains a 1:1 `conversation_compaction` table holding opaque JSON (it never imports `compaction`). Wiring (trigger, server swap, live summarizer, config) is the separate 2b-1b plan.

**Tech Stack:** Go; `compaction` (parts 1–2), `conversation`, `agent.BuildLLMHistory`, `contextmeter`.

## Global Constraints

- All server-side. The engine is **pure** over its inputs (no DB I/O, no goroutines); the store methods are the only persistence.
- Frozen segments are **never re-summarized** — `Advance` maps only NEW segments and reuses stored segment summaries.
- The trigger measures the **live tail**, never total size (anti-thrash). `Advance`'s gates: activation (total < floor → skip), cadence (eligible-to-freeze < one segment → skip).
- `BuildSendView` output is always pairing-valid (ends in part-1 `AssembleSendView` → `llm.RepairPairing`).
- The `conversation` package must NOT import `compaction` — it persists opaque JSON strings + ints.
- Build + test: `cd source/server && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain "Claude"; no `Co-Authored-By` trailer.

## Interfaces consumed (already on this branch)

- `compaction`: `StructuredSummary`, `SummarizeFunc`, `MergeSummaries`, `ElideSupersededToolResults`, `SegmentByTokens`, `AssembleSendView`, `MessageTokens`, `TotalTokens`, `renderSummaryMessages` (unexported — see Task 1).
- `agent.BuildLLMHistory(turns []conversation.Turn) []llm.Message`.
- `conversation.Turn{ ID, Role, Content, BlocksJSON string; CreatedAt time.Time; ... }`.
- `contextmeter.Tokenizer` / `contextmeter.Default()`.

---

## File Structure

- `source/server/internal/compaction/reduce.go` — `Reduce` (extracted from `mapreduce.go`).
- `source/server/internal/conversation/schema.sql` — add `conversation_compaction` table.
- `source/server/internal/conversation/store.go` — `Compaction` type + `GetCompaction`/`SaveCompaction` + interface entries.
- `source/server/internal/compactor/compactor.go` — `Config`, `Advance`, `BuildSendView`, JSON helpers.
- `source/server/internal/compactor/compactor_test.go` — engine tests (fake summarizer).

---

## Task 1: Extract `compaction.Reduce`

**Files:**
- Create: `source/server/internal/compaction/reduce.go`
- Modify: `source/server/internal/compaction/mapreduce.go` (call `Reduce`)
- Test: `source/server/internal/compaction/reduce_test.go`

**Interfaces:**
- Produces: `Reduce(ctx context.Context, parts []StructuredSummary, summarize SummarizeFunc) (StructuredSummary, error)` — for `len(parts) > 1` runs a model reduce over `renderSummaryMessages` of each part; otherwise returns `MergeSummaries(parts)`. (Exported so the stateful engine reuses C's reduce step.)

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compaction/reduce_test.go`:

```go
package compaction

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestReduce_SingleIsMerge_NoModel(t *testing.T) {
	calls := 0
	fake := func(context.Context, []llm.Message) (StructuredSummary, error) {
		calls++
		return StructuredSummary{}, nil
	}
	out, err := Reduce(context.Background(), []StructuredSummary{{Goal: "only"}}, fake)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("single part should not call the model, got %d calls", calls)
	}
	if out.Goal != "only" {
		t.Errorf("single part should pass through, got %q", out.Goal)
	}
}

func TestReduce_MultiCallsModelWithRenderedParts(t *testing.T) {
	var seen string
	fake := func(_ context.Context, m []llm.Message) (StructuredSummary, error) {
		for _, msg := range m {
			for _, b := range msg.Blocks {
				seen += b.Text
			}
		}
		return StructuredSummary{Goal: "reduced"}, nil
	}
	out, err := Reduce(context.Background(),
		[]StructuredSummary{{Goal: "g1"}, {Goal: "g2"}}, fake)
	if err != nil {
		t.Fatal(err)
	}
	if out.Goal != "reduced" {
		t.Errorf("multi part should use the model reduce, got %q", out.Goal)
	}
	if !strings.Contains(seen, "g1") || !strings.Contains(seen, "g2") {
		t.Errorf("reduce input should contain both rendered parts, saw: %s", seen)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compaction/ -run TestReduce -count=1`
Expected: FAIL — `Reduce` undefined.

- [ ] **Step 3: Create `reduce.go`**

```go
package compaction

import (
	"context"

	"cercano/source/server/internal/llm"
)

// Reduce reconciles segment summaries into one. With more than one part it runs
// a model reduce pass over the rendered parts (C's reduce step); with one (or
// zero) it falls back to the deterministic MergeSummaries.
func Reduce(ctx context.Context, parts []StructuredSummary, summarize SummarizeFunc) (StructuredSummary, error) {
	if len(parts) > 1 {
		var input []llm.Message
		for _, p := range parts {
			input = append(input, renderSummaryMessages(p)...)
		}
		return summarize(ctx, input)
	}
	return MergeSummaries(parts), nil
}
```

- [ ] **Step 4: Refactor `mapreduce.go` to use `Reduce`**

In `mapreduce.go`, replace the inline `if c.ModelReduce && len(parts) > 1 { … } else { … }` reduce/merge block with:

```go
		if c.ModelReduce {
			r, err := Reduce(ctx, parts, summarize)
			if err != nil {
				return Result{}, err
			}
			sum = r
		} else {
			sum = MergeSummaries(parts)
		}
```

`sum` is the existing result var; `r`/`err` are fresh locals (avoids assuming an
outer `err`). Keep the surrounding `len(older) > 0` guard. Behavior is unchanged:
B (ModelReduce=false) still always merges; C still model-reduces, and `Reduce`'s
own `len(parts)==1` fallback matches the old `len(parts) > 1` guard.

- [ ] **Step 5: Run reduce + mapreduce tests**

Run: `cd source/server && go test ./internal/compaction/ -run 'TestReduce|TestMapReduce' -count=1`
Expected: PASS (existing map-reduce behavior preserved).

- [ ] **Step 6: Commit**

```bash
cd source/server
git add internal/compaction/reduce.go internal/compaction/reduce_test.go internal/compaction/mapreduce.go
git commit -m "feat(server): extract compaction.Reduce (shared by map-reduce + stateful engine)"
```

---

## Task 2: Persistence — `conversation_compaction` table + store methods

**Files:**
- Modify: `source/server/internal/conversation/schema.sql`
- Modify: `source/server/internal/conversation/store.go` (`Compaction` type; interface; `GetCompaction`/`SaveCompaction`)
- Test: `source/server/internal/conversation/compaction_store_test.go`

**Interfaces:**
- Produces:
  - `conversation.Compaction{ ConversationID string; FrozenThrough int64; SegmentSummariesJSON string; ConsolidatedJSON string; CompactedTokens int; UpdatedAt time.Time }`
  - `Store.GetCompaction(ctx, conversationID string) (Compaction, error)` — zero value (FrozenThrough 0, empty JSON) when no row.
  - `Store.SaveCompaction(ctx, c Compaction) error` — upsert.

The store stays ignorant of summary structure — it persists opaque JSON strings.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/conversation/compaction_store_test.go`:

```go
package conversation

import (
	"context"
	"testing"
)

func TestCompaction_RoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}

	// Missing row → zero value, no error.
	zero, err := s.GetCompaction(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if zero.FrozenThrough != 0 || zero.SegmentSummariesJSON != "" {
		t.Errorf("missing compaction should be zero value, got %+v", zero)
	}

	in := Compaction{
		ConversationID:       "c1",
		FrozenThrough:        1700,
		SegmentSummariesJSON: `[{"Goal":"g"}]`,
		ConsolidatedJSON:     `{"Goal":"g"}`,
		CompactedTokens:      4096,
	}
	if err := s.SaveCompaction(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCompaction(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.FrozenThrough != 1700 || got.SegmentSummariesJSON != in.SegmentSummariesJSON ||
		got.ConsolidatedJSON != in.ConsolidatedJSON || got.CompactedTokens != 4096 {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Upsert overwrites.
	in.FrozenThrough = 1800
	if err := s.SaveCompaction(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetCompaction(ctx, "c1")
	if got.FrozenThrough != 1800 {
		t.Errorf("upsert should overwrite, got FrozenThrough=%d", got.FrozenThrough)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/conversation/ -run TestCompaction_RoundTrip -count=1`
Expected: FAIL — `Compaction` / `GetCompaction` / `SaveCompaction` undefined.

- [ ] **Step 3: Add the table to `schema.sql`**

Append to `source/server/internal/conversation/schema.sql`:

```sql
-- conversation_compaction: the derived compaction layer (1:1 with a
-- conversation). Holds opaque JSON summaries + the frozen boundary; raw turns
-- remain the source of truth. CREATE IF NOT EXISTS runs on every Open, so this
-- table is created for both fresh and pre-existing DBs (no separate migration).
CREATE TABLE IF NOT EXISTS conversation_compaction (
    conversation_id   TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    frozen_through    INTEGER NOT NULL DEFAULT 0,
    segment_summaries TEXT    NOT NULL DEFAULT '',
    consolidated      TEXT    NOT NULL DEFAULT '',
    compacted_tokens  INTEGER NOT NULL DEFAULT 0,
    updated_at        INTEGER NOT NULL DEFAULT 0
);
```

- [ ] **Step 4: Add the `Compaction` type + interface methods in `store.go`**

Add the type near `Info` (after the `Info` struct):

```go
// Compaction is the persisted derived compaction layer for a conversation.
// Summaries are opaque JSON here; the compactor package owns their structure.
type Compaction struct {
	ConversationID       string
	FrozenThrough        int64 // turns with CreatedAt.Unix() <= this are frozen
	SegmentSummariesJSON string
	ConsolidatedJSON     string
	CompactedTokens      int
	UpdatedAt            time.Time
}
```

Add to the `Store` interface (near `UpdateRecap`):

```go
	// GetCompaction returns the derived compaction state, or a zero value
	// (FrozenThrough 0, empty JSON) if none exists yet.
	GetCompaction(ctx context.Context, conversationID string) (Compaction, error)
	// SaveCompaction upserts the derived compaction state.
	SaveCompaction(ctx context.Context, c Compaction) error
```

- [ ] **Step 5: Implement the methods on `sqliteStore`**

Add near `UpdateRecap`:

```go
func (s *sqliteStore) GetCompaction(ctx context.Context, conversationID string) (Compaction, error) {
	if conversationID == "" {
		return Compaction{}, errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Compaction{ConversationID: conversationID}
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT frozen_through, segment_summaries, consolidated, compacted_tokens, updated_at
		 FROM conversation_compaction WHERE conversation_id = ?`, conversationID).
		Scan(&c.FrozenThrough, &c.SegmentSummariesJSON, &c.ConsolidatedJSON, &c.CompactedTokens, &updated)
	if err == sql.ErrNoRows {
		return Compaction{ConversationID: conversationID}, nil
	}
	if err != nil {
		return Compaction{}, err
	}
	c.UpdatedAt = time.Unix(updated, 0)
	return c, nil
}

func (s *sqliteStore) SaveCompaction(ctx context.Context, c Compaction) error {
	if c.ConversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_compaction
			(conversation_id, frozen_through, segment_summaries, consolidated, compacted_tokens, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET
			frozen_through=excluded.frozen_through,
			segment_summaries=excluded.segment_summaries,
			consolidated=excluded.consolidated,
			compacted_tokens=excluded.compacted_tokens,
			updated_at=excluded.updated_at`,
		c.ConversationID, c.FrozenThrough, c.SegmentSummariesJSON, c.ConsolidatedJSON,
		c.CompactedTokens, time.Now().Unix())
	return err
}
```

(Confirm `database/sql` is imported as `sql` in `store.go` — it is, used by `*sql.DB`.)

- [ ] **Step 6: Run the test + full build**

Run: `cd source/server && go test ./internal/conversation/ -run TestCompaction_RoundTrip -count=1`
Expected: PASS.
Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS (agent/server tests use the real store, which now satisfies the wider interface).

- [ ] **Step 7: Commit**

```bash
cd source/server
git add internal/conversation/schema.sql internal/conversation/store.go internal/conversation/compaction_store_test.go
git commit -m "feat(server): conversation_compaction derived-layer table + Get/SaveCompaction"
```

---

## Task 3: `compactor.BuildSendView` + `Config`

**Files:**
- Create: `source/server/internal/compactor/compactor.go`
- Test: `source/server/internal/compactor/compactor_test.go`

**Interfaces:**
- Produces:
  - `Config{ ActivationFloorTokens, SegmentTokens, VerbatimRecent int }` with `DefaultConfig()` (40000 / 8000 / 6).
  - `BuildSendView(turns []conversation.Turn, state conversation.Compaction) ([]llm.Message, error)` — when `state.ConsolidatedJSON` is non-empty, returns `AssembleSendView(consolidated, BuildLLMHistory(live))` where live = turns with `CreatedAt.Unix() > state.FrozenThrough`; otherwise `BuildLLMHistory(turns)`. Used by the request path (2b-1b).

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compactor/compactor_test.go`:

```go
package compactor

import (
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func turn(id, role, content string, at int64) conversation.Turn {
	return conversation.Turn{ID: id, Role: role, Content: content, CreatedAt: time.Unix(at, 0)}
}

func flat(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			b.WriteString(blk.Text)
			b.WriteString(blk.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestBuildSendView_NoStateIsFullHistory(t *testing.T) {
	turns := []conversation.Turn{turn("a", "user", "hello", 100), turn("b", "assistant", "hi", 101)}
	view, err := BuildSendView(turns, conversation.Compaction{})
	if err != nil {
		t.Fatal(err)
	}
	out := flat(view)
	if !strings.Contains(out, "hello") || !strings.Contains(out, "hi") {
		t.Errorf("no compaction state → full history, got:\n%s", out)
	}
}

func TestBuildSendView_WithStatePreamblePlusLiveTail(t *testing.T) {
	turns := []conversation.Turn{
		turn("a", "user", "OLD-FROZEN", 100),
		turn("b", "assistant", "ALSO-FROZEN", 150),
		turn("c", "user", "LIVE-TAIL", 200),
	}
	state := conversation.Compaction{
		FrozenThrough:    150, // turns at/before 150 are frozen (a, b)
		ConsolidatedJSON: `{"Goal":"SUMMARY-GOAL","State":"done"}`,
	}
	view, err := BuildSendView(turns, state)
	if err != nil {
		t.Fatal(err)
	}
	out := flat(view)
	if !strings.Contains(out, "SUMMARY-GOAL") {
		t.Error("expected consolidated summary preamble")
	}
	if !strings.Contains(out, "LIVE-TAIL") {
		t.Error("expected the live tail verbatim")
	}
	if strings.Contains(out, "OLD-FROZEN") || strings.Contains(out, "ALSO-FROZEN") {
		t.Error("frozen turns must NOT appear verbatim — they're in the summary")
	}
	if !llm.IsValidPairing(view) {
		t.Error("send-view must be pairing-valid")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compactor/ -run TestBuildSendView -count=1`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Create `compactor.go` (Config + BuildSendView)**

```go
// Package compactor orchestrates stateful, frozen-segment context compaction
// over conversation turns + persisted state, reusing the compaction primitives.
// It is pure over its inputs; persistence and triggering live in their callers.
package compactor

import (
	"encoding/json"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// Config holds the (configurable) compaction thresholds. Defaults are derived
// from the real-session corpus (612 sessions): activate at 40k, freeze 8k
// segments, keep 6 recent turns verbatim.
type Config struct {
	ActivationFloorTokens int
	SegmentTokens         int
	VerbatimRecent        int
}

func DefaultConfig() Config {
	return Config{ActivationFloorTokens: 40000, SegmentTokens: 8000, VerbatimRecent: 6}
}

// BuildSendView assembles the history to send: the consolidated summary preamble
// + the live tail (turns after the frozen boundary), or the full history when
// nothing is frozen yet. Always pairing-valid.
func BuildSendView(turns []conversation.Turn, state conversation.Compaction) ([]llm.Message, error) {
	if state.ConsolidatedJSON == "" {
		return agent.BuildLLMHistory(turns), nil
	}
	var consolidated compaction.StructuredSummary
	if err := json.Unmarshal([]byte(state.ConsolidatedJSON), &consolidated); err != nil {
		// Corrupt state → fail safe to full history.
		return agent.BuildLLMHistory(turns), nil
	}
	live := liveTurns(turns, state.FrozenThrough)
	return compaction.AssembleSendView(consolidated, agent.BuildLLMHistory(live)), nil
}

// liveTurns returns turns strictly after the frozen boundary.
func liveTurns(turns []conversation.Turn, frozenThrough int64) []conversation.Turn {
	out := make([]conversation.Turn, 0, len(turns))
	for _, t := range turns {
		if t.CreatedAt.Unix() > frozenThrough {
			out = append(out, t)
		}
	}
	return out
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/compactor/ -run TestBuildSendView -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compactor/compactor.go internal/compactor/compactor_test.go
git commit -m "feat(server): compactor.BuildSendView + Config (consolidated preamble + live tail)"
```

---

## Task 4: `compactor.Advance` (the stateful pass)

**Files:**
- Modify: `source/server/internal/compactor/compactor.go` (add `Advance`)
- Test: `source/server/internal/compactor/advance_test.go`

**Interfaces:**
- Consumes: `compaction.{SummarizeFunc, ElideSupersededToolResults, SegmentByTokens, Reduce, MessageTokens, TotalTokens}`, `agent.BuildLLMHistory`, `contextmeter.Tokenizer`.
- Produces: `Advance(ctx, turns []conversation.Turn, state conversation.Compaction, summarize compaction.SummarizeFunc, cfg Config, tok contextmeter.Tokenizer) (conversation.Compaction, bool, error)` — returns the (possibly updated) state and whether it changed. Gates: total < `ActivationFloorTokens` → unchanged; eligible-to-freeze < `SegmentTokens` → unchanged. Otherwise: elide + segment the eligible turns, map each NEW segment via `summarize`, append to the stored segment summaries, advance `FrozenThrough`, `Reduce` to the consolidated summary, and return changed=true. Frozen segments are reused (not re-summarized).

- [ ] **Step 1: Write the failing tests**

Create `source/server/internal/compactor/advance_test.go`:

```go
package compactor

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// recSummarize records how many times it's called and returns a marked summary.
type recSummarize struct{ n int }

func (r *recSummarize) fn(_ context.Context, _ []llm.Message) (compaction.StructuredSummary, error) {
	id := r.n
	r.n++
	return compaction.StructuredSummary{Goal: fmt.Sprintf("SEG%d", id)}, nil
}

// bigTurns builds n user turns each ~tokensEach tokens, created at 100+i.
func bigTurns(n, tokensEach int) []conversation.Turn {
	body := ""
	for len(body)/4 < tokensEach {
		body += "lorem ipsum dolor sit amet "
	}
	var ts []conversation.Turn
	for i := 0; i < n; i++ {
		ts = append(ts, conversation.Turn{
			ID: fmt.Sprintf("t%d", i), Role: "user", Content: body,
			CreatedAt: time.Unix(int64(100+i), 0),
		})
	}
	return ts
}

func TestAdvance_ActivationGateSkipsSmall(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 100000, SegmentTokens: 8000, VerbatimRecent: 6}
	rec := &recSummarize{}
	turns := bigTurns(4, 500) // ~2k tokens total, below the 100k floor
	_, changed, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if changed || rec.n != 0 {
		t.Errorf("below activation floor: expected no work, changed=%v calls=%d", changed, rec.n)
	}
}

func TestAdvance_FreezesSegmentsAndReuses(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	rec := &recSummarize{}
	// 12 turns × ~1000 tok = ~12k total; eligible = all but last 2 = 10 turns ~10k
	// → at 4k segments, ~3 new segments.
	turns := bigTurns(12, 1000)

	st, changed, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected compaction to run")
	}
	if rec.n < 2 {
		t.Fatalf("expected multiple segments mapped, got %d", rec.n)
	}
	if st.ConsolidatedJSON == "" {
		t.Error("expected a consolidated summary")
	}
	if st.FrozenThrough == 0 {
		t.Error("expected the frozen boundary to advance")
	}
	// Boundary must leave the last VerbatimRecent turns live.
	lastFrozenIdx := len(turns) - cfg.VerbatimRecent - 1
	if st.FrozenThrough != turns[lastFrozenIdx].CreatedAt.Unix() {
		t.Errorf("FrozenThrough=%d, want %d (last eligible turn)", st.FrozenThrough, turns[lastFrozenIdx].CreatedAt.Unix())
	}
	var segs []compaction.StructuredSummary
	_ = json.Unmarshal([]byte(st.SegmentSummariesJSON), &segs)
	firstRunSegs := len(segs)
	callsAfterFirst := rec.n

	// Second pass with NO new turns: nothing eligible past the boundary → no work,
	// and crucially the frozen segments are NOT re-summarized.
	st2, changed2, err := Advance(context.Background(), turns, st, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Error("second pass with no new turns should be a no-op")
	}
	if rec.n != callsAfterFirst {
		t.Errorf("frozen segments must not be re-summarized: calls went %d → %d", callsAfterFirst, rec.n)
	}
	var segs2 []compaction.StructuredSummary
	_ = json.Unmarshal([]byte(st2.SegmentSummariesJSON), &segs2)
	if len(segs2) != firstRunSegs {
		t.Errorf("segment count changed on no-op pass: %d → %d", firstRunSegs, len(segs2))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compactor/ -run TestAdvance -count=1`
Expected: FAIL — `Advance` undefined.

- [ ] **Step 3: Implement `Advance` in `compactor.go`**

Add to `compactor.go` (extend the import block with `context`, `encoding/json` already present, `cercano/source/server/internal/contextmeter`):

```go
// Advance runs one stateful compaction pass. It freezes new segments of the
// eligible (older, un-frozen) history and re-reduces; frozen segments are reused
// untouched. Returns the updated state and whether anything changed. Pure: no
// I/O. Gates: total < ActivationFloorTokens, or eligible < one SegmentTokens →
// unchanged.
func Advance(ctx context.Context, turns []conversation.Turn, state conversation.Compaction,
	summarize compaction.SummarizeFunc, cfg Config, tok contextmeter.Tokenizer) (conversation.Compaction, bool, error) {

	all := agent.BuildLLMHistory(turns)
	if compaction.TotalTokens(tok, all) < cfg.ActivationFloorTokens {
		return state, false, nil // activation gate
	}

	live := liveTurns(turns, state.FrozenThrough)
	if len(live) <= cfg.VerbatimRecent {
		return state, false, nil // nothing past the verbatim window
	}
	eligible := live[:len(live)-cfg.VerbatimRecent]

	eligibleMsgs := agent.BuildLLMHistory(eligible)
	if compaction.TotalTokens(tok, eligibleMsgs) < cfg.SegmentTokens {
		return state, false, nil // cadence gate — let the tail accumulate
	}

	// Map each new segment from raw (after mechanical elision).
	elided, _ := compaction.ElideSupersededToolResults(eligibleMsgs)
	var newParts []compaction.StructuredSummary
	for _, seg := range compaction.SegmentByTokens(elided, tok, cfg.SegmentTokens) {
		s, err := summarize(ctx, seg.Messages)
		if err != nil {
			return state, false, err
		}
		newParts = append(newParts, s)
	}

	// Reuse the already-frozen segment summaries; append the new ones.
	var parts []compaction.StructuredSummary
	if state.SegmentSummariesJSON != "" {
		if err := json.Unmarshal([]byte(state.SegmentSummariesJSON), &parts); err != nil {
			parts = nil // corrupt → rebuild from new
		}
	}
	parts = append(parts, newParts...)

	consolidated, err := compaction.Reduce(ctx, parts, summarize)
	if err != nil {
		return state, false, err
	}

	segJSON, _ := json.Marshal(parts)
	conJSON, _ := json.Marshal(consolidated)
	newState := conversation.Compaction{
		ConversationID:       state.ConversationID,
		FrozenThrough:        eligible[len(eligible)-1].CreatedAt.Unix(),
		SegmentSummariesJSON: string(segJSON),
		ConsolidatedJSON:     string(conJSON),
		CompactedTokens:      state.CompactedTokens + compaction.TotalTokens(tok, elided),
	}
	return newState, true, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd source/server && go test ./internal/compactor/ -run TestAdvance -count=1`
Expected: PASS.

- [ ] **Step 5: Full module build + suite**

Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd source/server
git add internal/compactor/compactor.go internal/compactor/advance_test.go
git commit -m "feat(server): compactor.Advance — stateful frozen-segment compaction pass"
```

---

## Self-Review

**Spec coverage** (against `compaction-2b1-livewiring-design.md`):
- §1 frozen boundary / trigger on live tail → Task 4 (`Advance` gates on `liveTurns` + eligible, never total beyond the activation floor). ✓
- §2 persistence (table, opaque JSON, Get/Save) → Task 2. ✓
- §4 compaction pass (activation gate, cadence gate, elide, segment, map new only, reuse frozen, reduce, advance boundary) → Task 4. ✓
- §5 request-path assembly (consolidated + live tail, full history when none) → Task 3 (`BuildSendView`). ✓
- §6 data-driven defaults (40k / 8k / 6) → Task 3 (`DefaultConfig`). ✓
- C's reduce reused → Task 1 (`Reduce`). ✓
- Frozen never re-summarized → Task 4 test asserts the fake isn't re-called on a no-op pass. ✓
- `conversation` does not import `compaction` → Task 2 stores opaque JSON strings only. ✓

**Deferred to 2b-1b** (the wiring plan): the background trigger/generator, the `server.go:1038` swap to `BuildSendView`, the production local `SummarizeFunc`, config plumbing, and the sync hard-override. This plan delivers the pure, tested engine + persistence those will call.

**Placeholder scan:** none — every step has complete code.

**Type consistency:** `Compaction` fields identical across Task 2 (definition), Task 3 (`BuildSendView` reads `ConsolidatedJSON`/`FrozenThrough`), Task 4 (`Advance` reads/writes `SegmentSummariesJSON`/`FrozenThrough`/`ConsolidatedJSON`/`CompactedTokens`). `Config` (Task 3) consumed by `Advance` (Task 4). `Reduce` signature (Task 1) matches the `Advance` call. `compaction.SummarizeFunc` used uniformly.
