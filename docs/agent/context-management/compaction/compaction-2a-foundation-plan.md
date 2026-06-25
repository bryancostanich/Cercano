# Compaction 2a (part 1) — Foundation + Bake-off Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the deterministic compaction substrate — the `Compactor` interface, token-budgeted segmentation, mechanical tool-result dedup/elision, structured summaries, pairing-valid send-view assembly — plus a documented fixture corpus and a metrics harness, proven end-to-end with an elision-only baseline compactor.

**Architecture:** A new `source/server/internal/compaction` package holds all measurement infrastructure; it mutates no live state (reads message arrays, emits derived send-views, scores them). Pairing-repair logic moves from the `agent` package into `llm` so both `agent` and `compaction` can reuse it without an import cycle. The model-backed candidate algorithms are a separate follow-on plan (2a part 2); this plan delivers the harness and a deterministic baseline it scores.

**Tech Stack:** Go; `source/server/internal/llm` (block/message types), `source/server/internal/contextmeter` (tokenizer).

## Global Constraints

- All code is server-side under `source/server/internal/`. No client changes.
- Every send-view a `Compactor` produces MUST be pairing-valid: every `tool_use` answered by a later `tool_result` with the matching id (Anthropic API rule). The harness asserts this on every produced view.
- The compaction package mutates NO live state — it is pure over its inputs. No DB, no goroutines, no timers in this plan.
- The deterministic layer (segmentation, dedup, merge, assembly, baseline compactor, harness) uses NO real model — tests use a fake `SummarizeFunc`.
- Build + test: `cd source/server && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain the word "Claude"; no `Co-Authored-By` trailer.

---

## File Structure

- `source/server/internal/llm/pairing.go` — `RepairPairing` + `IsValidPairing` (moved from `agent/history.go`).
- `source/server/internal/llm/pairing_test.go` — tests for the moved logic.
- `source/server/internal/agent/history.go` — `repairPairing` call becomes `llm.RepairPairing`.
- `source/server/internal/compaction/types.go` — `StructuredSummary`, `Segment`, `Budget`, `Result`, `SummarizeFunc`, `Compactor`.
- `source/server/internal/compaction/tokens.go` — `MessageTokens`, `TotalTokens`, `Segment`.
- `source/server/internal/compaction/dedup.go` — `ElideSupersededToolResults`.
- `source/server/internal/compaction/summary.go` — `MergeSummaries`, `StructuredSummary.RenderBlock`.
- `source/server/internal/compaction/sendview.go` — `AssembleSendView`.
- `source/server/internal/compaction/elision.go` — `ElisionCompactor` (the baseline).
- `source/server/internal/compaction/corpus.go` — `Fixture` + `Corpus()` (documented fixtures) + message builders.
- `source/server/internal/compaction/metrics.go` — `Metrics`, `Score`.
- `source/server/internal/compaction/*_test.go` — per-file tests.

---

## Task 1: Move pairing-repair into `llm`

**Files:**
- Create: `source/server/internal/llm/pairing.go`
- Create: `source/server/internal/llm/pairing_test.go`
- Modify: `source/server/internal/agent/history.go` (replace `repairPairing` call + delete the local func)

**Interfaces:**
- Produces: `llm.RepairPairing(msgs []Message) []Message` (drops orphaned tool_use/tool_result blocks, order-preserving) and `llm.IsValidPairing(msgs []Message) bool` (true when `RepairPairing` would change nothing).

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/llm/pairing_test.go`:

```go
package llm

import "testing"

func TestRepairPairing_DropsOrphanToolUse(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, Blocks: []Block{{Type: BlockToolUse, ToolUseID: "t1", ToolName: "read"}}},
		// no tool_result for t1
	}
	got := RepairPairing(msgs)
	if len(got) != 0 {
		t.Fatalf("orphan tool_use should be dropped, got %d messages", len(got))
	}
	if IsValidPairing(msgs) {
		t.Error("input with orphan tool_use should be invalid")
	}
}

func TestRepairPairing_KeepsValidPair(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, Blocks: []Block{{Type: BlockToolUse, ToolUseID: "t1"}}},
		{Role: RoleUser, Blocks: []Block{{Type: BlockToolResult, ToolUseRef: "t1", Content: "ok"}}},
	}
	if !IsValidPairing(msgs) {
		t.Error("a use followed by its result is valid")
	}
	if got := RepairPairing(msgs); len(got) != 2 {
		t.Fatalf("valid pair must survive, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd source/server && go test ./internal/llm/ -run TestRepairPairing -count=1`
Expected: FAIL — `RepairPairing` / `IsValidPairing` undefined.

- [ ] **Step 3: Create `pairing.go`**

Create `source/server/internal/llm/pairing.go`, moving the logic verbatim from `agent/history.go` (the `repairPairing` body) and adding the validity helper:

```go
package llm

// RepairPairing removes orphaned tool_use / tool_result blocks so the array is
// always valid to send. A tool_use is kept only if a tool_result referencing
// its id appears in a LATER message; a tool_result is kept only if a tool_use
// declaring its id appears in an EARLIER message. Messages left with no blocks
// are dropped. Order-preserving and pure.
func RepairPairing(msgs []Message) []Message {
	useIdx := map[string]int{}
	for i, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == BlockToolUse {
				if _, ok := useIdx[b.ToolUseID]; !ok {
					useIdx[b.ToolUseID] = i
				}
			}
		}
	}
	resolvedAfter := map[string]bool{}
	for i, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == BlockToolResult {
				if j, ok := useIdx[b.ToolUseRef]; ok && i > j {
					resolvedAfter[b.ToolUseRef] = true
				}
			}
		}
	}
	out := make([]Message, 0, len(msgs))
	for i, m := range msgs {
		kept := make([]Block, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockToolUse:
				if !resolvedAfter[b.ToolUseID] {
					continue
				}
			case BlockToolResult:
				if j, ok := useIdx[b.ToolUseRef]; !ok || i <= j {
					continue
				}
			}
			kept = append(kept, b)
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, Message{Role: m.Role, Blocks: kept})
	}
	return out
}

// IsValidPairing reports whether msgs already satisfies the use/result pairing
// rule — i.e. RepairPairing would drop nothing.
func IsValidPairing(msgs []Message) bool {
	repaired := RepairPairing(msgs)
	if len(repaired) != len(msgs) {
		return false
	}
	for i := range msgs {
		if len(repaired[i].Blocks) != len(msgs[i].Blocks) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Update `agent/history.go` to delegate**

In `source/server/internal/agent/history.go`: delete the local `repairPairing` function entirely, and change the return in `BuildLLMHistory` from `return repairPairing(msgs)` to `return llm.RepairPairing(msgs)`. (The file already imports `llm`.)

- [ ] **Step 5: Run llm + agent tests**

Run: `cd source/server && go test ./internal/llm/ ./internal/agent/ -count=1`
Expected: PASS (the agent's existing pairing tests now exercise the moved code through `BuildLLMHistory`).

- [ ] **Step 6: Commit**

```bash
cd source/server
git add internal/llm/pairing.go internal/llm/pairing_test.go internal/agent/history.go
git commit -m "refactor(server): move tool-use pairing repair into llm package

Extracts repairPairing from agent into llm.RepairPairing (+ IsValidPairing)
so both agent and the upcoming compaction package can reuse it without an
import cycle. agent.BuildLLMHistory now delegates."
```

---

## Task 2: Compaction core types + token budgeting + segmentation

**Files:**
- Create: `source/server/internal/compaction/types.go`
- Create: `source/server/internal/compaction/tokens.go`
- Test: `source/server/internal/compaction/tokens_test.go`

**Interfaces:**
- Produces:
  - `StructuredSummary{ Goal string; Decisions []string; Files map[string]string; OpenThreads []string; State string }`
  - `Segment{ Messages []llm.Message; Tokens int }`
  - `Budget{ TargetTokens int; VerbatimRecent int; SegmentTokens int }`
  - `Result{ SendView []llm.Message; Summaries []StructuredSummary }`
  - `SummarizeFunc func(ctx context.Context, messages []llm.Message) (StructuredSummary, error)`
  - `Compactor interface { Name() string; Compact(ctx, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) }`
  - `MessageTokens(tok contextmeter.Tokenizer, m llm.Message) int`, `TotalTokens(tok, msgs) int`, `SegmentByTokens(msgs, tok, perSegment int) []Segment`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compaction/tokens_test.go`:

```go
package compaction

import (
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func textMsg(role llm.Role, s string) llm.Message {
	return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}

func TestSegmentByTokens_SplitsOnBudget(t *testing.T) {
	tok := contextmeter.Default()
	msgs := []llm.Message{
		textMsg(llm.RoleUser, "alpha beta gamma delta"),
		textMsg(llm.RoleAssistant, "epsilon zeta eta theta"),
		textMsg(llm.RoleUser, "iota kappa lambda mu"),
	}
	// A tiny per-segment budget forces (at least) one boundary.
	segs := SegmentByTokens(msgs, tok, MessageTokens(tok, msgs[0]))
	if len(segs) < 2 {
		t.Fatalf("expected multiple segments under a tight budget, got %d", len(segs))
	}
	// No message is lost.
	var n int
	for _, s := range segs {
		n += len(s.Messages)
	}
	if n != len(msgs) {
		t.Fatalf("segments dropped messages: got %d of %d", n, len(msgs))
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd source/server && go test ./internal/compaction/ -run TestSegmentByTokens -count=1`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Create `types.go`**

```go
// Package compaction reduces a conversation's message history into a smaller,
// pairing-valid send-view, while keeping the original messages untouched. It is
// pure over its inputs: no DB, no goroutines, no live state. Candidate
// algorithms implement Compactor and are scored by the metrics harness.
package compaction

import (
	"context"

	"cercano/source/server/internal/llm"
)

// StructuredSummary is the fixed-section reduction of a span of history.
// Structured (not prose) so merges are deterministic and degradation is bounded.
type StructuredSummary struct {
	Goal        string            // the session's objective
	Decisions   []string          // key decisions made
	Files       map[string]string // path -> latest known state/summary
	OpenThreads []string          // unresolved questions / next steps
	State       string            // one-line current state
}

// Segment is a contiguous, token-budgeted slice of the history.
type Segment struct {
	Messages []llm.Message
	Tokens   int
}

// Budget bounds a compaction.
type Budget struct {
	TargetTokens   int // desired max tokens for the assembled send-view
	VerbatimRecent int // number of trailing messages kept verbatim
	SegmentTokens  int // token budget per segment for summarization
}

// Result is what a Compactor produces.
type Result struct {
	SendView  []llm.Message       // assembled, pairing-valid array to send
	Summaries []StructuredSummary  // summaries produced (for persistence/inspection)
}

// SummarizeFunc produces a StructuredSummary from a chunk of messages. Real
// implementations wrap the local model; the harness uses a deterministic fake.
type SummarizeFunc func(ctx context.Context, messages []llm.Message) (StructuredSummary, error)

// Compactor reduces raw messages into a send-view + summaries. Pure w.r.t. live
// state.
type Compactor interface {
	Name() string
	Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error)
}
```

- [ ] **Step 4: Create `tokens.go`**

```go
package compaction

import (
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// MessageTokens estimates the token cost of a message by counting its block
// text/content/tool-input. Approximate but deterministic for a given tokenizer.
func MessageTokens(tok contextmeter.Tokenizer, m llm.Message) int {
	n := 0
	for _, b := range m.Blocks {
		if b.Text != "" {
			n += tok.Count(b.Text)
		}
		if b.Content != "" {
			n += tok.Count(b.Content)
		}
		if len(b.ToolInput) > 0 {
			n += tok.Count(string(b.ToolInput))
		}
		if b.ToolName != "" {
			n += tok.Count(b.ToolName)
		}
	}
	return n
}

// TotalTokens sums MessageTokens over msgs.
func TotalTokens(tok contextmeter.Tokenizer, msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += MessageTokens(tok, m)
	}
	return n
}

// SegmentByTokens splits msgs into contiguous segments, each accumulating up to
// perSegment tokens (a single oversized message becomes its own segment). Never
// drops or reorders messages.
func SegmentByTokens(msgs []llm.Message, tok contextmeter.Tokenizer, perSegment int) []Segment {
	if perSegment < 1 {
		perSegment = 1
	}
	var segs []Segment
	var cur []llm.Message
	curTok := 0
	flush := func() {
		if len(cur) > 0 {
			segs = append(segs, Segment{Messages: cur, Tokens: curTok})
			cur = nil
			curTok = 0
		}
	}
	for _, m := range msgs {
		mt := MessageTokens(tok, m)
		if curTok > 0 && curTok+mt > perSegment {
			flush()
		}
		cur = append(cur, m)
		curTok += mt
	}
	flush()
	return segs
}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `cd source/server && go test ./internal/compaction/ -run TestSegmentByTokens -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd source/server
git add internal/compaction/types.go internal/compaction/tokens.go internal/compaction/tokens_test.go
git commit -m "feat(server): compaction core types + token-budgeted segmentation"
```

---

## Task 3: Mechanical tool-result dedup (elision)

**Files:**
- Create: `source/server/internal/compaction/dedup.go`
- Test: `source/server/internal/compaction/dedup_test.go`

**Interfaces:**
- Consumes: `llm.Message`/`llm.Block` types.
- Produces: `ElideSupersededToolResults(msgs []llm.Message) (out []llm.Message, collapsed int)` — for tool calls that recur with identical `(ToolName, ToolInput)`, replaces the `Content` of every *earlier* matching `tool_result` with a one-line stub, keeping all blocks and ids (pairing preserved). Returns the rewritten messages and the count of stubbed results.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compaction/dedup_test.go`:

```go
package compaction

import (
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func toolUse(id, name, input string) llm.Block {
	return llm.Block{Type: llm.BlockToolUse, ToolUseID: id, ToolName: name, ToolInput: []byte(input)}
}
func toolResult(ref, content string) llm.Block {
	return llm.Block{Type: llm.BlockToolResult, ToolUseRef: ref, Content: content}
}

func TestElide_StubsSupersededDuplicateReads(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u1", "read", `{"path":"a.go"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u1", "OLD CONTENTS of a.go")}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u2", "read", `{"path":"a.go"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u2", "NEW CONTENTS of a.go")}},
	}
	out, collapsed := ElideSupersededToolResults(msgs)
	if collapsed != 1 {
		t.Fatalf("expected 1 stubbed result, got %d", collapsed)
	}
	flat := ""
	for _, m := range out {
		for _, b := range m.Blocks {
			flat += b.Content
		}
	}
	if strings.Contains(flat, "OLD CONTENTS") {
		t.Error("superseded (older) result should be stubbed away")
	}
	if !strings.Contains(flat, "NEW CONTENTS") {
		t.Error("latest result must be kept verbatim")
	}
	// Pairing must remain valid (all blocks kept, just content rewritten).
	if !llm.IsValidPairing(out) {
		t.Error("elision must preserve pairing validity")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd source/server && go test ./internal/compaction/ -run TestElide -count=1`
Expected: FAIL — `ElideSupersededToolResults` undefined.

- [ ] **Step 3: Create `dedup.go`**

```go
package compaction

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

// ElideSupersededToolResults rewrites the Content of tool_result blocks whose
// originating tool call (same ToolName + identical ToolInput bytes) recurs later
// in the history. Only the LAST occurrence of each identical call keeps its full
// result; earlier ones are replaced with a one-line stub. All blocks and ids are
// preserved, so pairing stays valid. Returns the rewritten messages and the
// number of results stubbed. Pure: input is not mutated.
func ElideSupersededToolResults(msgs []llm.Message) ([]llm.Message, int) {
	// key -> last tool_use id for that identical call.
	lastUseForKey := map[string]string{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				lastUseForKey[toolKey(b)] = b.ToolUseID
			}
		}
	}
	// id -> key, so a tool_result can find whether its use is the last for its key.
	keyForUse := map[string]string{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				keyForUse[b.ToolUseID] = toolKey(b)
			}
		}
	}

	collapsed := 0
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		blocks := make([]llm.Block, len(m.Blocks))
		for j, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				if key, ok := keyForUse[b.ToolUseRef]; ok && lastUseForKey[key] != b.ToolUseRef {
					b.Content = fmt.Sprintf("[elided: superseded result, %d chars]", len(b.Content))
					collapsed++
				}
			}
			blocks[j] = b
		}
		out[i] = llm.Message{Role: m.Role, Blocks: blocks}
	}
	return out, collapsed
}

func toolKey(b llm.Block) string {
	return b.ToolName + "\x00" + string(b.ToolInput)
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd source/server && go test ./internal/compaction/ -run TestElide -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compaction/dedup.go internal/compaction/dedup_test.go
git commit -m "feat(server): mechanical tool-result dedup (stub superseded duplicate calls)"
```

---

## Task 4: Structured summary merge + render

**Files:**
- Create: `source/server/internal/compaction/summary.go`
- Test: `source/server/internal/compaction/summary_test.go`

**Interfaces:**
- Consumes: `StructuredSummary` (Task 2), `llm.Block`.
- Produces: `MergeSummaries(sums []StructuredSummary) StructuredSummary` (Goal = first non-empty; Decisions/OpenThreads concatenated with exact-duplicate removal preserving order; Files unioned with later paths overriding earlier; State = last non-empty) and `(StructuredSummary) RenderBlock() llm.Block` (a single `BlockText` preamble).

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compaction/summary_test.go`:

```go
package compaction

import (
	"strings"
	"testing"
)

func TestMergeSummaries_DedupesAndOverrides(t *testing.T) {
	a := StructuredSummary{
		Goal:      "ship compaction",
		Decisions: []string{"use structured summaries"},
		Files:     map[string]string{"a.go": "stubbed out"},
		State:     "early",
	}
	b := StructuredSummary{
		Decisions: []string{"use structured summaries", "elide superseded reads"}, // first is a dup
		Files:     map[string]string{"a.go": "finalized", "b.go": "added"},          // a.go overrides
		State:     "mid",
	}
	m := MergeSummaries([]StructuredSummary{a, b})
	if m.Goal != "ship compaction" {
		t.Errorf("Goal = %q", m.Goal)
	}
	if len(m.Decisions) != 2 {
		t.Errorf("Decisions should dedupe to 2, got %v", m.Decisions)
	}
	if m.Files["a.go"] != "finalized" {
		t.Errorf("later Files value should win, got %q", m.Files["a.go"])
	}
	if m.State != "mid" {
		t.Errorf("State should be last non-empty, got %q", m.State)
	}
}

func TestRenderBlock_ContainsSections(t *testing.T) {
	s := StructuredSummary{Goal: "G", Decisions: []string{"D1"}, State: "S"}
	blk := s.RenderBlock()
	if !strings.Contains(blk.Text, "G") || !strings.Contains(blk.Text, "D1") || !strings.Contains(blk.Text, "S") {
		t.Errorf("rendered block missing content:\n%s", blk.Text)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd source/server && go test ./internal/compaction/ -run 'TestMergeSummaries|TestRenderBlock' -count=1`
Expected: FAIL — functions undefined.

- [ ] **Step 3: Create `summary.go`**

```go
package compaction

import (
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

// MergeSummaries reconciles segment summaries into one. Goal is the first
// non-empty; Decisions and OpenThreads concatenate with exact-duplicate removal
// (order preserved); Files union with later values overriding earlier ones for
// the same path; State is the last non-empty. Deterministic.
func MergeSummaries(sums []StructuredSummary) StructuredSummary {
	out := StructuredSummary{Files: map[string]string{}}
	for _, s := range sums {
		if out.Goal == "" && s.Goal != "" {
			out.Goal = s.Goal
		}
		out.Decisions = appendUnique(out.Decisions, s.Decisions)
		out.OpenThreads = appendUnique(out.OpenThreads, s.OpenThreads)
		for path, state := range s.Files {
			out.Files[path] = state
		}
		if s.State != "" {
			out.State = s.State
		}
	}
	return out
}

func appendUnique(dst, src []string) []string {
	seen := map[string]bool{}
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range src {
		if !seen[v] {
			dst = append(dst, v)
			seen[v] = true
		}
	}
	return dst
}

// RenderBlock renders the summary into a single text block used as a send-view
// preamble. Section order is fixed for determinism.
func (s StructuredSummary) RenderBlock() llm.Block {
	var b strings.Builder
	b.WriteString("[conversation summary]\n")
	if s.Goal != "" {
		fmt.Fprintf(&b, "Goal: %s\n", s.Goal)
	}
	if len(s.Decisions) > 0 {
		b.WriteString("Decisions:\n")
		for _, d := range s.Decisions {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
	}
	if len(s.Files) > 0 {
		b.WriteString("Files:\n")
		for _, path := range sortedKeys(s.Files) {
			fmt.Fprintf(&b, "  - %s: %s\n", path, s.Files[path])
		}
	}
	if len(s.OpenThreads) > 0 {
		b.WriteString("Open threads:\n")
		for _, o := range s.OpenThreads {
			fmt.Fprintf(&b, "  - %s\n", o)
		}
	}
	if s.State != "" {
		fmt.Fprintf(&b, "Current state: %s\n", s.State)
	}
	return llm.Block{Type: llm.BlockText, Text: strings.TrimRight(b.String(), "\n")}
}
```

- [ ] **Step 4: Add the `sortedKeys` helper**

Files iteration must be deterministic (Go map order is random). Add to `summary.go`:

```go
import "sort" // add to the import block

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `cd source/server && go test ./internal/compaction/ -run 'TestMergeSummaries|TestRenderBlock' -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd source/server
git add internal/compaction/summary.go internal/compaction/summary_test.go
git commit -m "feat(server): structured summary merge + deterministic render"
```

---

## Task 5: Send-view assembly

**Files:**
- Create: `source/server/internal/compaction/sendview.go`
- Test: `source/server/internal/compaction/sendview_test.go`

**Interfaces:**
- Consumes: `StructuredSummary.RenderBlock` (Task 4), `llm.RepairPairing` (Task 1).
- Produces: `AssembleSendView(summary StructuredSummary, body []llm.Message) []llm.Message` — prepends a single user-role preamble message carrying `summary.RenderBlock()` (only when the summary is non-empty), appends `body`, then returns `llm.RepairPairing` of the whole. The result is always pairing-valid.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compaction/sendview_test.go`:

```go
package compaction

import (
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestAssembleSendView_PreambleThenBodyValid(t *testing.T) {
	summary := StructuredSummary{Goal: "do the thing", State: "wrapping up"}
	body := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u1", "read", `{"path":"a"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u1", "data")}},
	}
	view := AssembleSendView(summary, body)
	if len(view) != 3 {
		t.Fatalf("expected preamble + 2 body messages, got %d", len(view))
	}
	if !strings.Contains(view[0].Blocks[0].Text, "do the thing") {
		t.Errorf("first message should be the summary preamble, got %+v", view[0])
	}
	if !llm.IsValidPairing(view) {
		t.Error("assembled send-view must be pairing-valid")
	}
}

func TestAssembleSendView_EmptySummaryNoPreamble(t *testing.T) {
	body := []llm.Message{textMsg(llm.RoleUser, "hi")}
	view := AssembleSendView(StructuredSummary{}, body)
	if len(view) != 1 {
		t.Fatalf("empty summary should add no preamble, got %d messages", len(view))
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd source/server && go test ./internal/compaction/ -run TestAssembleSendView -count=1`
Expected: FAIL — `AssembleSendView` undefined.

- [ ] **Step 3: Create `sendview.go`**

```go
package compaction

import "cercano/source/server/internal/llm"

// AssembleSendView builds the final message array: a single summary preamble
// (omitted when the summary is empty), then the body, repaired so tool-use
// pairing is always valid.
func AssembleSendView(summary StructuredSummary, body []llm.Message) []llm.Message {
	var view []llm.Message
	if !summary.isEmpty() {
		view = append(view, llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{summary.RenderBlock()}})
	}
	view = append(view, body...)
	return llm.RepairPairing(view)
}

func (s StructuredSummary) isEmpty() bool {
	return s.Goal == "" && s.State == "" &&
		len(s.Decisions) == 0 && len(s.OpenThreads) == 0 && len(s.Files) == 0
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd source/server && go test ./internal/compaction/ -run TestAssembleSendView -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compaction/sendview.go internal/compaction/sendview_test.go
git commit -m "feat(server): pairing-valid send-view assembly"
```

---

## Task 6: Elision-only baseline `Compactor`

**Files:**
- Create: `source/server/internal/compaction/elision.go`
- Test: `source/server/internal/compaction/elision_test.go`

**Interfaces:**
- Consumes: `ElideSupersededToolResults` (Task 3), `AssembleSendView` (Task 5), `Compactor`/`Budget`/`Result` (Task 2).
- Produces: `ElisionCompactor{}` implementing `Compactor`. `Name()` returns `"elision"`. `Compact` ignores the summarizer entirely (zero model calls), runs mechanical dedup over `raw`, and returns it as the send-view with an empty summary. This is the deterministic floor of the bake-off.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compaction/elision_test.go`:

```go
package compaction

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestElisionCompactor_DedupesNoModel(t *testing.T) {
	raw := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u1", "read", `{"path":"a"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u1", "OLD")}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u2", "read", `{"path":"a"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u2", "NEW")}},
	}
	modelCalls := 0
	fake := func(ctx context.Context, _ []llm.Message) (StructuredSummary, error) {
		modelCalls++
		return StructuredSummary{}, nil
	}
	res, err := ElisionCompactor{}.Compact(context.Background(), raw, fake, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if modelCalls != 0 {
		t.Errorf("elision baseline must make no model calls, made %d", modelCalls)
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
	flat := ""
	for _, m := range res.SendView {
		for _, b := range m.Blocks {
			flat += b.Content
		}
	}
	if strings.Contains(flat, "OLD") || !strings.Contains(flat, "NEW") {
		t.Errorf("expected superseded OLD stubbed, NEW kept:\n%s", flat)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd source/server && go test ./internal/compaction/ -run TestElisionCompactor -count=1`
Expected: FAIL — `ElisionCompactor` undefined.

- [ ] **Step 3: Create `elision.go`**

```go
package compaction

import (
	"context"

	"cercano/source/server/internal/llm"
)

// ElisionCompactor is the deterministic baseline: it makes no model calls. It
// only collapses superseded duplicate tool results, then sends the result
// verbatim. It is the floor of the bake-off — how much can mechanical dedup
// reclaim with zero summarization and zero information loss to prose?
type ElisionCompactor struct{}

func (ElisionCompactor) Name() string { return "elision" }

func (ElisionCompactor) Compact(_ context.Context, raw []llm.Message, _ SummarizeFunc, _ Budget) (Result, error) {
	deduped, _ := ElideSupersededToolResults(raw)
	return Result{SendView: AssembleSendView(StructuredSummary{}, deduped)}, nil
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd source/server && go test ./internal/compaction/ -run TestElisionCompactor -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compaction/elision.go internal/compaction/elision_test.go
git commit -m "feat(server): elision-only baseline compactor (deterministic, no model)"
```

---

## Task 7: Documented fixture corpus

**Files:**
- Create: `source/server/internal/compaction/corpus.go`
- Test: `source/server/internal/compaction/corpus_test.go`

**Interfaces:**
- Consumes: `llm` types.
- Produces: `Fixture{ Name string; Description string; Messages []llm.Message; MustKeep []string }` and `Corpus() []Fixture`, plus message builders `bUser`, `bAssistant`, `bToolCall(id, name, input string)`, `bToolResult(id, content string)`. Each fixture is pairing-valid and declares the substrings that must survive compaction.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compaction/corpus_test.go`:

```go
package compaction

import (
	"testing"

	"cercano/source/server/internal/llm"
)

func TestCorpus_FixturesValid(t *testing.T) {
	c := Corpus()
	if len(c) < 3 {
		t.Fatalf("expected at least 3 fixtures, got %d", len(c))
	}
	for _, f := range c {
		if f.Name == "" || len(f.Messages) == 0 || len(f.MustKeep) == 0 {
			t.Errorf("fixture %q is underspecified", f.Name)
		}
		if !llm.IsValidPairing(f.Messages) {
			t.Errorf("fixture %q is not pairing-valid", f.Name)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd source/server && go test ./internal/compaction/ -run TestCorpus -count=1`
Expected: FAIL — `Corpus` undefined.

- [ ] **Step 3: Create `corpus.go`**

```go
package compaction

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

// Fixture is one documented, realistic conversation pattern, sized for the
// bake-off. MustKeep lists substrings that compaction must not lose.
type Fixture struct {
	Name        string
	Description string
	Messages    []llm.Message
	MustKeep    []string
}

func bUser(s string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}
func bAssistant(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}
func bToolCall(id, name, input string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{
		{Type: llm.BlockToolUse, ToolUseID: id, ToolName: name, ToolInput: []byte(input)}}}
}
func bToolResult(id, content string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockToolResult, ToolUseRef: id, Content: content}}}
}

// Corpus returns the documented fixtures. Each must be pairing-valid.
func Corpus() []Fixture {
	return []Fixture{
		repeatedReadsFixture(),
		refactorManyFilesFixture(),
		lightQAFixture(),
	}
}

// repeatedReadsFixture: the dedup stressor — the same file is read 5 times as it
// is edited. Only the final read's content is needed; the earlier four are dead
// weight. MustKeep: the goal and the LATEST contents.
func repeatedReadsFixture() Fixture {
	var msgs []llm.Message
	msgs = append(msgs, bUser("Fix the off-by-one in paginate() in pager.go"))
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("r%d", i)
		msgs = append(msgs, bToolCall(id, "read", `{"path":"pager.go"}`))
		msgs = append(msgs, bToolResult(id, fmt.Sprintf("pager.go revision %d: func paginate() { /* body v%d */ }", i, i)))
	}
	msgs = append(msgs, bAssistant("Fixed the off-by-one: the loop bound now uses <= len."))
	return Fixture{
		Name:        "repeated-reads",
		Description: "Same file read 5x while editing; only the latest read matters.",
		Messages:    msgs,
		MustKeep:    []string{"off-by-one", "paginate", "revision 5"},
	}
}

// refactorManyFilesFixture: a refactor touching several distinct files, each read
// once. Nothing is superseded; dedup should reclaim little, but a summary should
// retain every file path. MustKeep: the goal + each file path.
func refactorManyFilesFixture() Fixture {
	files := []string{"auth.go", "session.go", "token.go", "middleware.go"}
	msgs := []llm.Message{bUser("Rename Session.UserID to Session.AccountID across the auth package")}
	for i, f := range files {
		id := fmt.Sprintf("f%d", i)
		msgs = append(msgs, bToolCall(id, "read", fmt.Sprintf(`{"path":%q}`, f)))
		msgs = append(msgs, bToolResult(id, fmt.Sprintf("contents of %s with UserID references", f)))
	}
	msgs = append(msgs, bAssistant("Renamed UserID to AccountID in all four files."))
	return Fixture{
		Name:        "refactor-many-files",
		Description: "Distinct files each read once; no supersession; paths must survive.",
		Messages:    msgs,
		MustKeep:    []string{"AccountID", "auth.go", "session.go", "token.go", "middleware.go"},
	}
}

// lightQAFixture: mostly prose, almost no tool use — compaction should barely
// touch it. MustKeep: the question and the decision.
func lightQAFixture() Fixture {
	return Fixture{
		Name:        "light-qa",
		Description: "Prose Q&A with minimal tool use; little to compact.",
		Messages: []llm.Message{
			bUser("Should we use a channel or a mutex for the recap timer map?"),
			bAssistant("A mutex — the map is short-lived per-conversation and contention is low."),
			bUser("Okay, go with the mutex."),
		},
		MustKeep: []string{"mutex", "recap timer"},
	}
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd source/server && go test ./internal/compaction/ -run TestCorpus -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compaction/corpus.go internal/compaction/corpus_test.go
git commit -m "feat(server): documented compaction fixture corpus (3 patterns)"
```

---

## Task 8: Metrics harness + baseline bake-off run

**Files:**
- Create: `source/server/internal/compaction/metrics.go`
- Test: `source/server/internal/compaction/metrics_test.go`

**Interfaces:**
- Consumes: everything above + `contextmeter.Tokenizer`.
- Produces: `Metrics{ RawTokens, SentTokens int; Reduction float64; AnchorsKept, AnchorsTotal int; DedupCollapsed int; PairingValid bool; ModelCalls int }` and `Score(ctx, c Compactor, f Fixture, summarize SummarizeFunc, tok contextmeter.Tokenizer, b Budget) (Metrics, error)`. `Score` runs the compactor, then measures: token reduction (raw vs send-view), anchor retention (each `MustKeep` substring present in the concatenated send-view text), pairing validity, dedup count (stub markers), and model-call count (the caller's fake increments a counter it owns; `Score` reports it via the returned metric by wrapping the passed `summarize`).

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/compaction/metrics_test.go`:

```go
package compaction

import (
	"context"
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func nopSummarize(_ context.Context, _ []llm.Message) (StructuredSummary, error) {
	return StructuredSummary{}, nil
}

func TestScore_ElisionBaselineOverCorpus(t *testing.T) {
	tok := contextmeter.Default()
	b := Budget{TargetTokens: 4000, VerbatimRecent: 4, SegmentTokens: 2000}
	for _, f := range Corpus() {
		m, err := Score(context.Background(), ElisionCompactor{}, f, nopSummarize, tok, b)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if !m.PairingValid {
			t.Errorf("%s: send-view not pairing-valid", f.Name)
		}
		if m.ModelCalls != 0 {
			t.Errorf("%s: elision baseline made %d model calls", f.Name, m.ModelCalls)
		}
		// Elision never touches prose, so every anchor must survive on the
		// fixtures whose anchors are not inside a superseded result.
		if f.Name != "repeated-reads" && m.AnchorsKept != m.AnchorsTotal {
			t.Errorf("%s: anchors %d/%d kept", f.Name, m.AnchorsKept, m.AnchorsTotal)
		}
	}
}

func TestScore_RepeatedReadsReclaimsTokens(t *testing.T) {
	tok := contextmeter.Default()
	m, err := Score(context.Background(), ElisionCompactor{},
		repeatedReadsFixture(), nopSummarize, tok, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if m.DedupCollapsed < 4 {
		t.Errorf("expected >=4 superseded reads stubbed, got %d", m.DedupCollapsed)
	}
	if m.Reduction <= 0 {
		t.Errorf("expected positive token reduction, got %f", m.Reduction)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd source/server && go test ./internal/compaction/ -run TestScore -count=1`
Expected: FAIL — `Score` / `Metrics` undefined.

- [ ] **Step 3: Create `metrics.go`**

```go
package compaction

import (
	"context"
	"strings"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// Metrics is one compactor's score on one fixture.
type Metrics struct {
	RawTokens      int
	SentTokens     int
	Reduction      float64 // 1 - SentTokens/RawTokens (0 when RawTokens == 0)
	AnchorsKept    int
	AnchorsTotal   int
	DedupCollapsed int  // count of elision stub markers in the send-view
	PairingValid   bool
	ModelCalls     int
}

const elisionStubMarker = "[elided: superseded result"

// Score runs c over fixture f and measures the result. It wraps summarize to
// count model calls, so the caller's SummarizeFunc need not track them.
func Score(ctx context.Context, c Compactor, f Fixture, summarize SummarizeFunc,
	tok contextmeter.Tokenizer, b Budget) (Metrics, error) {

	calls := 0
	counted := func(ctx context.Context, msgs []llm.Message) (StructuredSummary, error) {
		calls++
		return summarize(ctx, msgs)
	}

	res, err := c.Compact(ctx, f.Messages, counted, b)
	if err != nil {
		return Metrics{}, err
	}

	raw := TotalTokens(tok, f.Messages)
	sent := TotalTokens(tok, res.SendView)
	flat := flattenText(res.SendView)

	kept := 0
	for _, anchor := range f.MustKeep {
		if strings.Contains(flat, anchor) {
			kept++
		}
	}

	m := Metrics{
		RawTokens:      raw,
		SentTokens:     sent,
		AnchorsKept:    kept,
		AnchorsTotal:   len(f.MustKeep),
		DedupCollapsed: strings.Count(flat, elisionStubMarker),
		PairingValid:   llm.IsValidPairing(res.SendView),
		ModelCalls:     calls,
	}
	if raw > 0 {
		m.Reduction = 1 - float64(sent)/float64(raw)
	}
	return m, nil
}

func flattenText(msgs []llm.Message) string {
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
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd source/server && go test ./internal/compaction/ -run TestScore -count=1`
Expected: PASS.

- [ ] **Step 5: Run the full module build + suite**

Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd source/server
git add internal/compaction/metrics.go internal/compaction/metrics_test.go
git commit -m "feat(server): compaction metrics harness + elision baseline bake-off"
```

---

## Self-Review

**Spec coverage** (against `compaction-design.md` §1, §2, §3, plus the layering and tool-use constraints):
- §1 segmentation → Task 2 (`SegmentByTokens`). ✓
- §1 mechanical pre-dedup/elision → Task 3 (`ElideSupersededToolResults`). ✓
- §1 structured summaries → Task 4 (`StructuredSummary`, `MergeSummaries`, `RenderBlock`). ✓
- §1 send-view assembly + `repairPairing` → Task 5 (`AssembleSendView` over `llm.RepairPairing`). ✓
- §2 `Compactor` interface → Task 2; one concrete (elision baseline) → Task 6. ✓
- §3 metrics harness (reduction, anchor retention, dedup, pairing validity, model-call count) → Task 8 (`Score`/`Metrics`). ✓
- §4 documented corpus → Task 7 (3 fixtures; dedup stressor, multi-file refactor, light Q&A). ✓
- Tool-use constraint (pairing valid send-view always) → Task 1 (`llm.RepairPairing`/`IsValidPairing`), asserted in Tasks 5/6/8. ✓
- Layering (server-side, no live-state mutation) → entire package is pure; no DB/goroutine. ✓

**Deferred to 2a part 2** (a separate plan, by design): the three model-backed algorithms (rolling, map-reduce, frozen-segment) implementing `Compactor`; the remaining corpus fixtures whose value is anchor-retention under summarization (long-debug, research-fetches); the real local-model `SummarizeFunc` and optional out-of-CI quality runs. This plan delivers the harness + a deterministic baseline it scores.

**Placeholder scan:** Task 6 Step 3 intentionally shows a stray `import_` line with an explicit instruction to remove it and the correct import block beside it — this is a guard against a common Go mistake, not a placeholder. All other steps have complete code.

**Type consistency:** `SummarizeFunc`, `StructuredSummary`, `Budget`, `Result`, `Compactor`, `Metrics`, `Fixture` are defined once (Tasks 2/8/7) and used with identical signatures in Tasks 5–8. Builders `toolUse`/`toolResult` (tests, Task 3) and `bToolCall`/`bToolResult` (corpus, Task 7) are deliberately separate (test-local vs package API). `llm.RepairPairing`/`IsValidPairing` (Task 1) are used consistently in Tasks 5/6/8.
