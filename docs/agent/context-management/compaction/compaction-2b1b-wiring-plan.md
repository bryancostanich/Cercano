# Compaction 2b-1b — Live Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make compaction live — a config block, the production local-model summarizer, a debounced background generator that runs `compactor.Advance` and persists, the agent schedule hook, and finally the `server.go` request-path swap (with a synchronous hard-limit override).

**Architecture:** A `compactiongen.Generator` (mirrors `recap.Generator`) debounces per-conversation compaction off the request path, calling the 2b-1a `compactor.Advance` and saving the derived state. The agent gains a `ScheduleCompaction` hook fired wherever turns persist. The request path swaps `agent.BuildLLMHistory(turns)` for `compactor.BuildSendView(turns, state)`; a synchronous `CompactNow` runs only when the assembled history would exceed the model's hard limit. Tasks 1–4 do NOT change what any request sends (background only); Task 5 flips the request path.

**Tech Stack:** Go; the 2b-1a `compactor` package, `compaction` (summarizer), `conversation` store, `contextmeter.ModelMax`, the `recap` generator pattern.

## Global Constraints

- All server-side. Tasks 1–4 are non-behavior-changing for live requests (background compaction only); Task 5 is the single behavior change.
- The summarizer runs the **local model directly** (like recap's `CompleteFunc`), never `StreamChat`. Off the request path except the sync override.
- Compaction failures never block a turn or a request — they fall back to the prior state / full history.
- Defaults (from the corpus): activation 40000, segment 8000, verbatim 6, hard-override 0.9 (of `ModelMax`).
- Build + test: `cd source/server && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain "Claude"; no `Co-Authored-By` trailer.

---

## File Structure

- `source/server/pkg/config/config.go` — `CompactionConfig` sub-struct + `Compaction` field + defaults.
- `source/server/internal/compactiongen/compactiongen.go` — `Generator` (Schedule / CompactNow / runCompaction).
- `source/server/internal/compactiongen/compactiongen_test.go`.
- `source/server/internal/agent/agent.go` — `CompactionScheduler` iface + `WithCompactionScheduler` + `ScheduleCompaction`/`CompactNow` + fire after a persisted turn.
- `source/server/internal/server/server.go` — request-path swap + sync override + `ScheduleCompaction` after the streaming turn persists.
- `source/server/cmd/cercano/main.go` — build the summarizer + generator, attach.

---

## Task 1: Config — `CompactionConfig`

**Files:**
- Modify: `source/server/pkg/config/config.go` (add the sub-struct, the `Compaction` field, defaults)
- Test: `source/server/pkg/config/config_test.go` (defaults assertion — add if a config test exists; else create)

**Interfaces:**
- Produces: `config.CompactionConfig{ Enabled bool; ActivationFloorTokens, SegmentTokens, VerbatimRecent int; HardOverridePct float64 }` and `Config.Compaction CompactionConfig`, defaulted to `{true, 40000, 8000, 6, 0.9}`.

- [ ] **Step 1: Write the failing test**

Create or append to `source/server/pkg/config/config_test.go`:

```go
package config

import "testing"

func TestDefaults_Compaction(t *testing.T) {
	c := Defaults()
	if !c.Compaction.Enabled {
		t.Error("compaction should default to enabled")
	}
	if c.Compaction.ActivationFloorTokens != 40000 ||
		c.Compaction.SegmentTokens != 8000 ||
		c.Compaction.VerbatimRecent != 6 {
		t.Errorf("compaction defaults = %+v", c.Compaction)
	}
	if c.Compaction.HardOverridePct != 0.9 {
		t.Errorf("hard override = %v, want 0.9", c.Compaction.HardOverridePct)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./pkg/config/ -run TestDefaults_Compaction -count=1`
Expected: FAIL — `Compaction` field undefined.

- [ ] **Step 3: Add the struct + field + defaults**

In `config.go`, add the struct (near `LlamaServerConfig`):

```go
// CompactionConfig controls background context compaction. Thresholds are token
// counts; HardOverridePct is a fraction of the cloud model's max context above
// which the request path compacts synchronously.
type CompactionConfig struct {
	Enabled               bool    `yaml:"enabled"`
	ActivationFloorTokens int     `yaml:"activation_floor_tokens"`
	SegmentTokens         int     `yaml:"segment_tokens"`
	VerbatimRecent        int     `yaml:"verbatim_recent"`
	HardOverridePct       float64 `yaml:"hard_override_pct"`
}
```

Add to the `Config` struct:

```go
	Compaction   CompactionConfig  `yaml:"compaction"`
```

In `Defaults()`, set:

```go
		Compaction: CompactionConfig{
			Enabled:               true,
			ActivationFloorTokens: 40000,
			SegmentTokens:         8000,
			VerbatimRecent:        6,
			HardOverridePct:       0.9,
		},
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./pkg/config/ -run TestDefaults_Compaction -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add pkg/config/config.go pkg/config/config_test.go
git commit -m "feat(server): CompactionConfig (enable + corpus-derived thresholds)"
```

---

## Task 2: The compaction generator

**Files:**
- Create: `source/server/internal/compactiongen/compactiongen.go`
- Test: `source/server/internal/compactiongen/compactiongen_test.go`

**Interfaces:**
- Produces:
  - `Store` interface: `GetTurns(ctx, convID) ([]conversation.Turn, error)`, `GetCompaction(ctx, convID) (conversation.Compaction, error)`, `SaveCompaction(ctx, conversation.Compaction) error`.
  - `Generator` with `New(store Store, summarize compaction.SummarizeFunc, cfg compactor.Config, tok contextmeter.Tokenizer, debounce time.Duration) *Generator`, `Schedule(convID string)` (debounced), `CompactNow(ctx, convID string) error` (synchronous). Both run `runCompaction`, which loads turns + state, calls `compactor.Advance`, and `SaveCompaction` only when it changed.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compactiongen/compactiongen_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compactiongen/ -count=1`
Expected: FAIL — package/`New` undefined.

- [ ] **Step 3: Create `compactiongen.go`**

```go
// Package compactiongen debounces per-conversation context compaction off the
// request path (mirrors the recap generator). It calls compactor.Advance and
// persists the derived state; failures are swallowed so a turn is never blocked.
package compactiongen

import (
	"context"
	"sync"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
)

// Store is the subset of conversation.Store the generator needs.
type Store interface {
	GetTurns(ctx context.Context, conversationID string) ([]conversation.Turn, error)
	GetCompaction(ctx context.Context, conversationID string) (conversation.Compaction, error)
	SaveCompaction(ctx context.Context, c conversation.Compaction) error
}

const runTimeout = 2 * time.Minute

// Generator debounces compaction per conversation.
type Generator struct {
	store     Store
	summarize compaction.SummarizeFunc
	cfg       compactor.Config
	tok       contextmeter.Tokenizer
	debounce  time.Duration

	mu     sync.Mutex
	timers map[string]*time.Timer
}

func New(store Store, summarize compaction.SummarizeFunc, cfg compactor.Config, tok contextmeter.Tokenizer, debounce time.Duration) *Generator {
	return &Generator{
		store: store, summarize: summarize, cfg: cfg, tok: tok, debounce: debounce,
		timers: make(map[string]*time.Timer),
	}
}

// Schedule requests a debounced compaction pass; rapid calls coalesce.
func (g *Generator) Schedule(conversationID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.timers[conversationID]; ok {
		t.Reset(g.debounce)
		return
	}
	g.timers[conversationID] = time.AfterFunc(g.debounce, func() {
		g.mu.Lock()
		delete(g.timers, conversationID)
		g.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()
		_ = g.runCompaction(ctx, conversationID)
	})
}

// CompactNow runs a compaction pass synchronously (used by the request-path
// hard-limit override).
func (g *Generator) CompactNow(ctx context.Context, conversationID string) error {
	return g.runCompaction(ctx, conversationID)
}

func (g *Generator) runCompaction(ctx context.Context, conversationID string) error {
	turns, err := g.store.GetTurns(ctx, conversationID)
	if err != nil || len(turns) == 0 {
		return err
	}
	state, err := g.store.GetCompaction(ctx, conversationID)
	if err != nil {
		return err
	}
	state.ConversationID = conversationID
	newState, changed, err := compactor.Advance(ctx, turns, state, g.summarize, g.cfg, g.tok)
	if err != nil || !changed {
		return err
	}
	return g.store.SaveCompaction(ctx, newState)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/compactiongen/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compactiongen/compactiongen.go internal/compactiongen/compactiongen_test.go
git commit -m "feat(server): compaction generator (debounced Schedule + synchronous CompactNow)"
```

---

## Task 3: Agent schedule hook

**Files:**
- Modify: `source/server/internal/agent/agent.go` (interface, option, methods, fire site)
- Test: `source/server/internal/agent/agent_test.go` (nil-safe + delegates)

**Interfaces:**
- Produces: `agent.CompactionScheduler` interface (`Schedule(convID string)`, `CompactNow(ctx context.Context, convID string) error`); `WithCompactionScheduler(CompactionScheduler) AgentOption`; `(*Agent).ScheduleCompaction(convID string)` (nil-safe); `(*Agent).CompactNow(ctx, convID) error` (nil-safe → nil). `ScheduleCompaction` is called after a persisted assistant turn, beside `ScheduleRecap`.

- [ ] **Step 1: Write the failing test**

Append to `source/server/internal/agent/agent_test.go`:

```go
type fakeCompactionScheduler struct {
	scheduled []string
	nowCalls  int
}

func (f *fakeCompactionScheduler) Schedule(id string) { f.scheduled = append(f.scheduled, id) }
func (f *fakeCompactionScheduler) CompactNow(_ context.Context, _ string) error {
	f.nowCalls++
	return nil
}

func TestScheduleCompaction_NilSafeAndDelegates(t *testing.T) {
	// Nil-safe: no scheduler attached.
	a := NewAgent(nil, nil)
	a.ScheduleCompaction("c1") // must not panic
	if err := a.CompactNow(context.Background(), "c1"); err != nil {
		t.Errorf("nil CompactNow should be a no-op nil, got %v", err)
	}

	fc := &fakeCompactionScheduler{}
	a2 := NewAgent(nil, nil, WithCompactionScheduler(fc))
	a2.ScheduleCompaction("c1")
	_ = a2.CompactNow(context.Background(), "c1")
	if len(fc.scheduled) != 1 || fc.scheduled[0] != "c1" {
		t.Errorf("Schedule not delegated: %v", fc.scheduled)
	}
	if fc.nowCalls != 1 {
		t.Errorf("CompactNow not delegated: %d", fc.nowCalls)
	}
}
```

(Ensure `context` is imported in the test file — it is used by other tests there.)

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/agent/ -run TestScheduleCompaction -count=1`
Expected: FAIL — `WithCompactionScheduler` / `ScheduleCompaction` / `CompactNow` undefined.

- [ ] **Step 3: Add the interface, field, option, methods**

In `agent.go`, near `RecapScheduler` / the `recap` field:

```go
// CompactionScheduler requests background context compaction for a conversation
// and can run it synchronously. Implemented by *compactiongen.Generator; an
// interface so agent doesn't import the generator package.
type CompactionScheduler interface {
	Schedule(conversationID string)
	CompactNow(ctx context.Context, conversationID string) error
}
```

Add the field to the `Agent` struct (beside `recap RecapScheduler`):

```go
	compaction CompactionScheduler
```

Add the option (beside `WithRecapScheduler`):

```go
// WithCompactionScheduler attaches the background compaction generator.
func WithCompactionScheduler(cs CompactionScheduler) AgentOption {
	return func(a *Agent) { a.compaction = cs }
}
```

Add the methods (beside `ScheduleRecap`):

```go
// ScheduleCompaction requests a debounced compaction pass. Nil-safe.
func (a *Agent) ScheduleCompaction(conversationID string) {
	if a.compaction != nil {
		a.compaction.Schedule(conversationID)
	}
}

// CompactNow runs a synchronous compaction pass. Nil-safe (returns nil).
func (a *Agent) CompactNow(ctx context.Context, conversationID string) error {
	if a.compaction != nil {
		return a.compaction.CompactNow(ctx, conversationID)
	}
	return nil
}
```

- [ ] **Step 4: Fire it after the persisted turn**

In `agent.go`, right after the existing `a.ScheduleRecap(conversationID)` (~line 208):

```go
		a.ScheduleRecap(conversationID)
		a.ScheduleCompaction(conversationID)
```

- [ ] **Step 5: Run the test + full build**

Run: `cd source/server && go test ./internal/agent/ -run TestScheduleCompaction -count=1`
Expected: PASS.
Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd source/server
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat(server): agent compaction schedule hook (Schedule + CompactNow, nil-safe)"
```

---

## Task 4: Wire the generator in `main.go`

**Files:**
- Modify: `source/server/cmd/cercano/main.go` (build summarizer + generator, attach)

**Interfaces:**
- Consumes: `compactiongen.New`, `agent.WithCompactionScheduler`, `compaction.{BuildSummaryPrompt, ParseSummary}`, `compactor.Config`, `contextmeter.Default`, `config.CompactionConfig`.

- [ ] **Step 1: Add the wiring**

In `main.go`, beside the recap wiring (the `if persistentStore != nil { … recap … }` block), add — gated on the persistent store AND `cfg.Compaction.Enabled`:

```go
	if persistentStore != nil && cfg.Compaction.Enabled {
		compactSummarize := func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
			resp, err := localProvider.Process(ctx, &agent.Request{Input: compaction.BuildSummaryPrompt(msgs)})
			if err != nil {
				return compaction.StructuredSummary{}, err
			}
			return compaction.ParseSummary(resp.Output), nil
		}
		compCfg := compactor.Config{
			ActivationFloorTokens: cfg.Compaction.ActivationFloorTokens,
			SegmentTokens:         cfg.Compaction.SegmentTokens,
			VerbatimRecent:        cfg.Compaction.VerbatimRecent,
		}
		compGen := compactiongen.New(persistentStore, compactSummarize, compCfg, contextmeter.Default(), 10*time.Second)
		agentOpts = append(agentOpts, agent.WithCompactionScheduler(compGen))
	}
```

Add the imports `cercano/source/server/internal/compaction`, `.../internal/compactor`, `.../internal/compactiongen`, `.../internal/contextmeter`, `.../internal/llm` (some may already be present — let the compiler tell you).

- [ ] **Step 2: Build**

Run: `cd source/server && go build ./... && go vet ./cmd/cercano/`
Expected: builds clean. (No unit test for `main`; the generator + agent hook are tested in Tasks 2–3.)

- [ ] **Step 3: Full suite**

Run: `cd source/server && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
cd source/server
git add cmd/cercano/main.go
git commit -m "feat(server): wire background compaction generator into the agent"
```

---

## Task 5: Request-path swap + sync override (the behavior change)

**Files:**
- Modify: `source/server/internal/server/server.go` (`:1033-1040` history assembly; `:1132` schedule)

**Interfaces:**
- Consumes: `compactor.BuildSendView`, `store.GetCompaction`, `compaction.TotalTokens`, `contextmeter.{Default, ModelMax}`, `s.agent.CompactNow`, `s.currentConfig.{CloudModel, Compaction}`.

- [ ] **Step 1: Swap the history assembly**

Replace the `:1033-1040` block:

```go
	var convHistory []llm.Message
	if store := s.agent.PersistentStore(); store != nil && req.GetConversationId() != "" {
		convHistory = s.assembleHistory(ctx, store, req.GetConversationId())
	}
	injectedLen := len(convHistory)
```

- [ ] **Step 2: Add the `assembleHistory` helper**

Add a method on the server (same file), implementing the swap + sync override:

```go
// assembleHistory builds the conversation history to send: the compacted view
// (consolidated summary + live tail) when compaction state exists, else the full
// history. If the assembled history exceeds the hard-override fraction of the
// model's max context, it compacts synchronously once and reassembles.
func (s *Server) assembleHistory(ctx context.Context, store conversation.Store, convID string) []llm.Message {
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] GetTurns(%s) failed: %v\n", convID, err)
		return nil
	}
	state, _ := store.GetCompaction(ctx, convID)
	view, _ := compactor.BuildSendView(turns, state)

	pct := s.currentConfig.Compaction.HardOverridePct
	if s.currentConfig.Compaction.Enabled && pct > 0 {
		hardLimit := int(float64(contextmeter.ModelMax(s.currentConfig.CloudModel)) * pct)
		if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
			if err := s.agent.CompactNow(ctx, convID); err == nil {
				state, _ = store.GetCompaction(ctx, convID)
				view, _ = compactor.BuildSendView(turns, state)
			}
		}
	}
	return view
}
```

(Imports to add to `server.go`: `cercano/source/server/internal/compactor`, `.../internal/compaction`, `.../internal/contextmeter`. `conversation` and `llm` are already imported.)

- [ ] **Step 3: Schedule compaction after the streaming turn persists**

After the existing `s.agent.ScheduleRecap(convID)` (~line 1132):

```go
	s.agent.ScheduleRecap(convID)
	s.agent.ScheduleCompaction(convID)
```

- [ ] **Step 4: Build + vet + full suite**

Run: `cd source/server && go build ./... && go vet ./internal/server/ && go test ./... -count=1`
Expected: builds clean; all green. (The request path is integration-tested manually; the engine, generator, and assembly logic are unit-tested in 2b-1a + Tasks 2–3. `BuildSendView` with no state returns the full history unchanged, so a fresh/small conversation behaves exactly as before.)

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/server/server.go
git commit -m "feat(server): send compacted history (consolidated summary + live tail) with sync hard-override"
```

---

## Self-Review

**Spec coverage** (against `compaction-2b1-livewiring-design.md`):
- §3 production summarizer (local model, BuildSummaryPrompt + ParseSummary) → Task 4. ✓
- §4 background trigger (debounced generator, Schedule after a turn) → Tasks 2, 3. ✓
- §4 sync hard-override → Task 5 (`assembleHistory` + `CompactNow`). ✓
- §5 request-path swap (`BuildSendView`, full history when none) → Task 5. ✓
- §6 data-driven defaults → Task 1 (config defaults). ✓
- Layering (agent doesn't import the generator; generator behind an interface) → Task 3. ✓
- Failures never block → generator swallows errors (Task 2); override falls back to the un-recompacted view (Task 5). ✓

**Ordering / risk:** Tasks 1–4 add background compaction + config + the hook but DO NOT change what any request sends. Task 5 is the only behavior change, and its `BuildSendView`-with-no-state path is the prior full-history behavior, so unconfigured / small / fresh conversations are unaffected.

**Placeholder scan:** none — every step has complete code. (Imports flagged "let the compiler tell you" are concrete additions, not placeholders.)

**Type consistency:** `compactor.Config` built from `config.CompactionConfig` (Tasks 1, 4) with matching field names. `compaction.SummarizeFunc` signature matches the `compactSummarize` closure (Task 4) and the generator (Task 2). `CompactionScheduler` (Task 3) matches `*compactiongen.Generator`'s `Schedule`/`CompactNow` (Task 2). `assembleHistory` (Task 5) uses `BuildSendView`/`GetCompaction`/`TotalTokens`/`ModelMax`/`CompactNow` with their real signatures.

**Deferred (2b-2 / 2b-3):** retention enforcement; `/c` "compacted N · live M" + show-original; explicit-trigger RPC. Hierarchical re-reduce when segment summaries grow large.
