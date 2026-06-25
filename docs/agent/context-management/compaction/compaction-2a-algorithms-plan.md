# Compaction 2a (part 2) — Summarizer + Algorithms + Bake-off Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the section-tagged summarizer (prompt + lenient parser), the three model-backed contender `Compactor`s (rolling, map-reduce/mechanical, map-reduce/model), two corpus fixtures, and the standalone agent-backed bake-off runner that scores them.

**Architecture:** Builds on the part-1 `compaction` package. The summarizer is a prompt builder + a lenient section parser (deterministic, CI-tested). Each contender implements the part-1 `Compactor` interface over one shared pipeline (elision → split off the recent verbatim window → summarize the older history its own way → `AssembleSendView`). The real model is reached only by the standalone `cmd/compaction-bakeoff`, which wraps the cercano agent (`agentclient.StreamChat`) as the `SummarizeFunc`; the algorithms and all CI tests use a fake summarizer.

**Tech Stack:** Go; the part-1 `compaction` package; `contextmeter`; `agentclient` (for the runner only).

## Global Constraints

- All code is server-side. The algorithms and parser are PURE and deterministic; only `cmd/compaction-bakeoff` touches a live model, and it is NOT part of the test suite.
- Every contender's send-view MUST be pairing-valid — guaranteed by ending in part-1's `AssembleSendView` (which runs `llm.RepairPairing`).
- The parser is LENIENT: a missing/garbled section yields an empty field, never a hard error.
- CI stays deterministic: algorithm structure is tested with a fake `SummarizeFunc`; no test requires a running model.
- Build + test: `cd source/server && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain the word "Claude"; no `Co-Authored-By` trailer.

## Interfaces consumed from part 1 (already on this branch)

- `StructuredSummary{ Goal string; Decisions []string; Files map[string]string; OpenThreads []string; State string }`
- `Budget{ TargetTokens, VerbatimRecent, SegmentTokens int }`; `Result{ SendView []llm.Message; Summaries []StructuredSummary }`
- `SummarizeFunc func(ctx, []llm.Message) (StructuredSummary, error)`; `Compactor{ Name() string; Compact(ctx, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) }`
- `ElideSupersededToolResults(msgs) ([]llm.Message, int)`; `SegmentByTokens(msgs, tok, perSegment) []Segment`; `MergeSummaries(sums) StructuredSummary`; `(StructuredSummary) RenderBlock() llm.Block`; `AssembleSendView(summary, body) []llm.Message`; `Score(...)`; `Corpus() []Fixture`.

---

## File Structure

- `source/server/internal/compaction/summarizer.go` — `BuildSummaryPrompt`, `ParseSummary`, `splitRecent`, `renderSummaryMessages`.
- `source/server/internal/compaction/summarizer_test.go` — parser tests.
- `source/server/internal/compaction/rolling.go` — `RollingCompactor`.
- `source/server/internal/compaction/rolling_test.go`.
- `source/server/internal/compaction/mapreduce.go` — `MapReduceCompactor` (mechanical + model reduce).
- `source/server/internal/compaction/mapreduce_test.go`.
- `source/server/internal/compaction/corpus.go` — extend with two fixtures.
- `source/server/cmd/compaction-bakeoff/main.go` — the standalone runner.

---

## Task 1: Section-tagged summarizer prompt + lenient parser

**Files:**
- Create: `source/server/internal/compaction/summarizer.go`
- Test: `source/server/internal/compaction/summarizer_test.go`

**Interfaces:**
- Produces:
  - `BuildSummaryPrompt(messages []llm.Message) string` — renders the messages to a transcript and appends the section-format instruction.
  - `ParseSummary(text string) StructuredSummary` — lenient parser of the section-tagged format.
  - `splitRecent(msgs []llm.Message, n int) (older, recent []llm.Message)` — splits off the last `n` messages (n<=0 → all older; n>=len → all recent).
  - `renderSummaryMessages(s StructuredSummary) []llm.Message` — wraps `s.RenderBlock()` as a single user message (empty slice when the summary is empty), for feeding a prior summary back into the model.

- [ ] **Step 1: Write the failing parser tests**

Create `source/server/internal/compaction/summarizer_test.go`:

```go
package compaction

import "testing"

func TestParseSummary_WellFormed(t *testing.T) {
	in := `GOAL: ship compaction
DECISIONS:
- use structured summaries
- elide superseded reads
FILES:
- a.go: finalized
- b.go: added
OPEN:
- wire 2b trigger
STATE: bake-off ready`
	s := ParseSummary(in)
	if s.Goal != "ship compaction" {
		t.Errorf("Goal = %q", s.Goal)
	}
	if len(s.Decisions) != 2 || s.Decisions[0] != "use structured summaries" {
		t.Errorf("Decisions = %v", s.Decisions)
	}
	if s.Files["a.go"] != "finalized" || s.Files["b.go"] != "added" {
		t.Errorf("Files = %v", s.Files)
	}
	if len(s.OpenThreads) != 1 || s.OpenThreads[0] != "wire 2b trigger" {
		t.Errorf("OpenThreads = %v", s.OpenThreads)
	}
	if s.State != "bake-off ready" {
		t.Errorf("State = %q", s.State)
	}
}

func TestParseSummary_MissingSectionsAndPreamble(t *testing.T) {
	in := `Sure! Here is the summary you asked for:

GOAL: fix the pager bug
STATE: fixed`
	s := ParseSummary(in)
	if s.Goal != "fix the pager bug" {
		t.Errorf("Goal = %q (preamble should be ignored)", s.Goal)
	}
	if s.State != "fixed" {
		t.Errorf("State = %q", s.State)
	}
	if len(s.Decisions) != 0 || len(s.Files) != 0 || len(s.OpenThreads) != 0 {
		t.Errorf("absent sections must be empty: %+v", s)
	}
}

func TestParseSummary_GarbageIsEmpty(t *testing.T) {
	s := ParseSummary("the model rambled with no sections at all")
	if s.Goal != "" || s.State != "" || len(s.Decisions) != 0 {
		t.Errorf("garbage must parse to empty summary, got %+v", s)
	}
}

func TestSplitRecent(t *testing.T) {
	msgs := []llm.Message{
		textMsg(llm.RoleUser, "1"), textMsg(llm.RoleAssistant, "2"), textMsg(llm.RoleUser, "3"),
	}
	older, recent := splitRecent(msgs, 1)
	if len(older) != 2 || len(recent) != 1 || recent[0].Blocks[0].Text != "3" {
		t.Errorf("splitRecent(1): older=%d recent=%d", len(older), len(recent))
	}
	older, recent = splitRecent(msgs, 10)
	if len(older) != 0 || len(recent) != 3 {
		t.Errorf("splitRecent(>len) should be all recent")
	}
}
```

(`textMsg` already exists in `tokens_test.go`; `llm` is imported there — add the `llm` import to this test file too.)

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compaction/ -run 'TestParseSummary|TestSplitRecent' -count=1`
Expected: FAIL — `ParseSummary` / `splitRecent` undefined.

- [ ] **Step 3: Create `summarizer.go`**

```go
package compaction

import (
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

// BuildSummaryPrompt renders messages to a transcript and asks the model for a
// fixed section-tagged summary. The format is parsed by ParseSummary.
func BuildSummaryPrompt(messages []llm.Message) string {
	var b strings.Builder
	b.WriteString("Summarize the following conversation span for later reference.\n")
	b.WriteString("Respond ONLY in this exact format, omitting a section if empty:\n\n")
	b.WriteString("GOAL: <one line: the objective>\n")
	b.WriteString("DECISIONS:\n- <key decision>\n")
	b.WriteString("FILES:\n- <path>: <latest state>\n")
	b.WriteString("OPEN:\n- <unresolved thread>\n")
	b.WriteString("STATE: <one line: current state>\n\n")
	b.WriteString("--- conversation ---\n")
	for _, m := range messages {
		for _, blk := range m.Blocks {
			switch blk.Type {
			case llm.BlockText:
				fmt.Fprintf(&b, "%s: %s\n", m.Role, blk.Text)
			case llm.BlockToolUse:
				fmt.Fprintf(&b, "%s: [tool %s %s]\n", m.Role, blk.ToolName, string(blk.ToolInput))
			case llm.BlockToolResult:
				fmt.Fprintf(&b, "%s: [tool result] %s\n", m.Role, blk.Content)
			}
		}
	}
	return b.String()
}

var summaryLabels = map[string]bool{
	"GOAL": true, "DECISIONS": true, "FILES": true, "OPEN": true, "STATE": true,
}

// ParseSummary leniently extracts the section-tagged summary. Unknown/leading
// prose is ignored; a missing section yields an empty field; it never errors.
func ParseSummary(text string) StructuredSummary {
	s := StructuredSummary{Files: map[string]string{}}
	section := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		label, rest, hasLabel := splitLabel(line)
		if hasLabel {
			section = label
			switch label {
			case "GOAL":
				s.Goal = strings.TrimSpace(rest)
			case "STATE":
				s.State = strings.TrimSpace(rest)
			}
			continue
		}
		item := stripBullet(line)
		if item == "" {
			continue
		}
		switch section {
		case "DECISIONS":
			s.Decisions = append(s.Decisions, item)
		case "OPEN":
			s.OpenThreads = append(s.OpenThreads, item)
		case "FILES":
			if path, state, ok := strings.Cut(item, ":"); ok {
				s.Files[strings.TrimSpace(path)] = strings.TrimSpace(state)
			}
		}
	}
	return s
}

// splitLabel reports whether line begins with a known SECTION: label, returning
// the upper-case label and the inline remainder.
func splitLabel(line string) (label, rest string, ok bool) {
	head, tail, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}
	up := strings.ToUpper(strings.TrimSpace(head))
	if summaryLabels[up] {
		return up, tail, true
	}
	return "", "", false
}

// stripBullet removes a leading "-", "*", or "N." marker.
func stripBullet(line string) string {
	line = strings.TrimSpace(line)
	for _, p := range []string{"- ", "* "} {
		if strings.HasPrefix(line, p) {
			return strings.TrimSpace(line[len(p):])
		}
	}
	// "1." / "2)" style
	if i := strings.IndexAny(line, ".)"); i > 0 && i <= 3 {
		if _, err := fmt.Sscanf(line[:i], "%d", new(int)); err == nil {
			return strings.TrimSpace(line[i+1:])
		}
	}
	return line
}

// splitRecent splits off the last n messages as the verbatim window.
func splitRecent(msgs []llm.Message, n int) (older, recent []llm.Message) {
	if n <= 0 {
		return msgs, nil
	}
	if n >= len(msgs) {
		return nil, msgs
	}
	return msgs[:len(msgs)-n], msgs[len(msgs)-n:]
}

// renderSummaryMessages wraps a non-empty summary as a single user message, for
// feeding a prior summary back into the model (rolling).
func renderSummaryMessages(s StructuredSummary) []llm.Message {
	if s.isEmpty() {
		return nil
	}
	return []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{s.RenderBlock()}}}
}
```

(`isEmpty` already exists on `StructuredSummary` from part 1's `sendview.go`.)

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/compaction/ -run 'TestParseSummary|TestSplitRecent' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compaction/summarizer.go internal/compaction/summarizer_test.go
git commit -m "feat(server): section-tagged summary prompt + lenient parser"
```

---

## Task 2: Rolling compactor (contender A)

**Files:**
- Create: `source/server/internal/compaction/rolling.go`
- Test: `source/server/internal/compaction/rolling_test.go`

**Interfaces:**
- Consumes: `BuildSummaryPrompt`/`splitRecent`/`renderSummaryMessages` (Task 1), `ElideSupersededToolResults`/`SegmentByTokens`/`AssembleSendView` (part 1), `contextmeter.Default`.
- Produces: `RollingCompactor{}` implementing `Compactor`; `Name()` returns `"rolling"`. `Compact` elides, splits off the recent window, segments the older history, and folds it sequentially: `sum = summarize(renderSummaryMessages(sum) ++ segment)` for each segment.

- [ ] **Step 1: Write the failing structure test**

Create `source/server/internal/compaction/rolling_test.go`:

```go
package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// recordingSummarizer returns a uniquely-marked summary per call and records the
// flattened input it received, so tests can assert how an algorithm fed it.
type recordingSummarizer struct {
	n      int
	inputs []string
}

func (r *recordingSummarizer) fn(_ context.Context, msgs []llm.Message) (StructuredSummary, error) {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			b.WriteString(blk.Text)
			b.WriteString(blk.Content)
			b.WriteByte('\n')
		}
	}
	r.inputs = append(r.inputs, b.String())
	id := r.n
	r.n++
	return StructuredSummary{Goal: fmt.Sprintf("SUM%d", id), State: fmt.Sprintf("SUM%d", id)}, nil
}

func TestRolling_ThreadsPriorSummaryAndKeepsRecent(t *testing.T) {
	// 6 older text messages + 2 recent; tiny segment budget forces >1 segment.
	var raw []llm.Message
	for i := 0; i < 6; i++ {
		raw = append(raw, textMsg(llm.RoleUser, fmt.Sprintf("older message number %d here", i)))
	}
	raw = append(raw, textMsg(llm.RoleAssistant, "RECENT-A"), textMsg(llm.RoleUser, "RECENT-B"))

	rec := &recordingSummarizer{}
	res, err := RollingCompactor{}.Compact(context.Background(), raw, rec.fn,
		Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	if rec.n < 2 {
		t.Fatalf("expected multiple sequential summarize calls, got %d", rec.n)
	}
	// The 2nd+ call must contain the prior summary marker (threading).
	if !strings.Contains(rec.inputs[1], "SUM0") {
		t.Errorf("rolling did not thread prior summary into call 2:\n%s", rec.inputs[1])
	}
	// Recent window kept verbatim in the send-view.
	flat := flattenText(res.SendView)
	if !strings.Contains(flat, "RECENT-A") || !strings.Contains(flat, "RECENT-B") {
		t.Errorf("recent window not kept verbatim:\n%s", flat)
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compaction/ -run TestRolling -count=1`
Expected: FAIL — `RollingCompactor` undefined.

- [ ] **Step 3: Create `rolling.go`**

```go
package compaction

import (
	"context"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// RollingCompactor folds the older history into a running summary, one segment
// at a time, carrying the prior summary forward. Sequential; exhibits
// compounding loss — the baseline the map-reduce contenders must beat.
type RollingCompactor struct{}

func (RollingCompactor) Name() string { return "rolling" }

func (RollingCompactor) Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) {
	elided, _ := ElideSupersededToolResults(raw)
	older, recent := splitRecent(elided, b.VerbatimRecent)

	var sum StructuredSummary
	if len(older) > 0 {
		tok := contextmeter.Default()
		for _, seg := range SegmentByTokens(older, tok, segTokens(b)) {
			input := append(renderSummaryMessages(sum), seg.Messages...)
			s, err := summarize(ctx, input)
			if err != nil {
				return Result{}, err
			}
			sum = s
		}
	}
	return Result{SendView: AssembleSendView(sum, recent), Summaries: []StructuredSummary{sum}}, nil
}

// segTokens returns the per-segment token budget, defaulting when unset.
func segTokens(b Budget) int {
	if b.SegmentTokens > 0 {
		return b.SegmentTokens
	}
	return 32000
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/compaction/ -run TestRolling -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compaction/rolling.go internal/compaction/rolling_test.go
git commit -m "feat(server): rolling compactor (sequential fold, contender A)"
```

---

## Task 3: Map-reduce compactors (contenders B and C)

**Files:**
- Create: `source/server/internal/compaction/mapreduce.go`
- Test: `source/server/internal/compaction/mapreduce_test.go`

**Interfaces:**
- Consumes: same as Task 2, plus `MergeSummaries` (part 1) and `BuildSummaryPrompt`/`renderSummaryMessages`.
- Produces: `MapReduceCompactor{ ModelReduce bool }` implementing `Compactor`. `Name()` returns `"map-reduce/mechanical"` (ModelReduce=false) or `"map-reduce/model"` (true). `Compact` maps each segment from raw independently, then reduces: mechanical (`MergeSummaries`) or a model reduce pass over `renderSummaryMessages` of all segment summaries.

- [ ] **Step 1: Write the failing structure tests**

Create `source/server/internal/compaction/mapreduce_test.go`:

```go
package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func mapReduceRaw() []llm.Message {
	var raw []llm.Message
	for i := 0; i < 6; i++ {
		raw = append(raw, textMsg(llm.RoleUser, fmt.Sprintf("older message number %d here", i)))
	}
	return append(raw, textMsg(llm.RoleAssistant, "RECENT-A"), textMsg(llm.RoleUser, "RECENT-B"))
}

func TestMapReduce_Mechanical_NoPriorThreadingNoExtraCall(t *testing.T) {
	rec := &recordingSummarizer{}
	res, err := MapReduceCompactor{ModelReduce: false}.Compact(
		context.Background(), mapReduceRaw(), rec.fn, Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	// Each map call sees only a raw segment — never a prior SUM marker.
	for i, in := range rec.inputs {
		if strings.Contains(in, "SUM") {
			t.Errorf("map call %d saw a prior summary (should be raw only):\n%s", i, in)
		}
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}

func TestMapReduce_Model_AddsReduceCall(t *testing.T) {
	recMech := &recordingSummarizer{}
	_, _ = MapReduceCompactor{ModelReduce: false}.Compact(
		context.Background(), mapReduceRaw(), recMech.fn, Budget{VerbatimRecent: 2, SegmentTokens: 6})

	recModel := &recordingSummarizer{}
	_, err := MapReduceCompactor{ModelReduce: true}.Compact(
		context.Background(), mapReduceRaw(), recModel.fn, Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	// The model-reduce variant makes exactly one MORE call (the reduce pass).
	if recModel.n != recMech.n+1 {
		t.Errorf("model reduce should add one call: mechanical=%d model=%d", recMech.n, recModel.n)
	}
	// The final (reduce) call's input contains the prior segment summary markers.
	last := recModel.inputs[len(recModel.inputs)-1]
	if !strings.Contains(last, "SUM") {
		t.Errorf("reduce call should receive segment summaries:\n%s", last)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compaction/ -run TestMapReduce -count=1`
Expected: FAIL — `MapReduceCompactor` undefined.

- [ ] **Step 3: Create `mapreduce.go`**

```go
package compaction

import (
	"context"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// MapReduceCompactor summarizes each older segment from raw (no compounding),
// then reduces the segment summaries — mechanically (MergeSummaries) or via a
// second model pass (ModelReduce).
type MapReduceCompactor struct {
	ModelReduce bool
}

func (c MapReduceCompactor) Name() string {
	if c.ModelReduce {
		return "map-reduce/model"
	}
	return "map-reduce/mechanical"
}

func (c MapReduceCompactor) Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) {
	elided, _ := ElideSupersededToolResults(raw)
	older, recent := splitRecent(elided, b.VerbatimRecent)

	var sum StructuredSummary
	if len(older) > 0 {
		tok := contextmeter.Default()
		var parts []StructuredSummary
		for _, seg := range SegmentByTokens(older, tok, segTokens(b)) {
			s, err := summarize(ctx, seg.Messages)
			if err != nil {
				return Result{}, err
			}
			parts = append(parts, s)
		}
		if c.ModelReduce && len(parts) > 1 {
			// Reduce pass: hand the rendered segment summaries back to the model.
			var input []llm.Message
			for _, p := range parts {
				input = append(input, renderSummaryMessages(p)...)
			}
			s, err := summarize(ctx, input)
			if err != nil {
				return Result{}, err
			}
			sum = s
		} else {
			sum = MergeSummaries(parts)
		}
	}
	return Result{SendView: AssembleSendView(sum, recent), Summaries: []StructuredSummary{sum}}, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/compaction/ -run TestMapReduce -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compaction/mapreduce.go internal/compaction/mapreduce_test.go
git commit -m "feat(server): map-reduce compactors, mechanical + model reduce (B and C)"
```

---

## Task 4: Corpus additions (long-debug, research-fetches)

**Files:**
- Modify: `source/server/internal/compaction/corpus.go` (add two fixtures + register them in `Corpus()`)
- Test: `source/server/internal/compaction/corpus_test.go` (extend the count assertion)

**Interfaces:**
- Consumes: the `b*` builders from part 1's `corpus.go`.
- Produces: `longDebugFixture()`, `researchFetchesFixture()`, both appended to `Corpus()`.

- [ ] **Step 1: Update the failing count test**

In `corpus_test.go`, raise the minimum-fixture assertion from `< 3` to `< 5`:

```go
	if len(c) < 5 {
		t.Fatalf("expected at least 5 fixtures, got %d", len(c))
	}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compaction/ -run TestCorpus -count=1`
Expected: FAIL — only 3 fixtures registered.

- [ ] **Step 3: Add the two fixtures in `corpus.go`**

Append to the `Corpus()` slice: `longDebugFixture(), researchFetchesFixture(),` and add:

```go
// longDebugFixture: a long debugging session that revisits one hypothesis across
// many turns, reading the same file repeatedly, before the real root cause is
// found. Tests whether the goal and the FINAL root cause survive summarization.
func longDebugFixture() Fixture {
	var msgs []llm.Message
	msgs = append(msgs, bUser("Tests flake intermittently in TestPager — find why"))
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("d%d", i)
		msgs = append(msgs, bToolCall(id, "read", `{"path":"pager_test.go"}`))
		msgs = append(msgs, bToolResult(id, fmt.Sprintf("pager_test.go inspection pass %d", i)))
		msgs = append(msgs, bAssistant(fmt.Sprintf("Hypothesis %d: maybe a timing issue; not confirmed.", i)))
	}
	msgs = append(msgs, bAssistant("Root cause found: a shared map is written without a lock in paginate()."))
	return Fixture{
		Name:        "long-debug",
		Description: "Long debug session revisiting one hypothesis; final root cause must survive.",
		Messages:    msgs,
		MustKeep:    []string{"flake", "TestPager", "shared map", "without a lock"},
	}
}

// researchFetchesFixture: many distinct web fetches, each a different finding.
// Tests whether distinct facts are retained rather than blurred together.
func researchFetchesFixture() Fixture {
	findings := []struct{ url, fact string }{
		{"a.example/rram", "RRAM endurance is ~10^6 cycles"},
		{"b.example/sram", "SRAM bitcell is 6T, ~0.2 um^2 in this node"},
		{"c.example/mram", "MRAM retention exceeds 10 years at 85C"},
		{"d.example/flash", "Flash needs ~18V for erase"},
	}
	msgs := []llm.Message{bUser("Compare emerging memory technologies for the edge accelerator")}
	for i, f := range findings {
		id := fmt.Sprintf("w%d", i)
		msgs = append(msgs, bToolCall(id, "fetch", fmt.Sprintf(`{"url":%q}`, f.url)))
		msgs = append(msgs, bToolResult(id, f.fact))
	}
	msgs = append(msgs, bAssistant("Compiled the comparison across RRAM, SRAM, MRAM, and Flash."))
	return Fixture{
		Name:        "research-fetches",
		Description: "Many distinct fetches; each finding must be retained, not blurred.",
		Messages:    msgs,
		MustKeep:    []string{"RRAM", "10 years", "18V", "6T"},
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/compaction/ -run TestCorpus -count=1`
Expected: PASS (5 fixtures, all pairing-valid).

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/compaction/corpus.go internal/compaction/corpus_test.go
git commit -m "feat(server): add long-debug + research-fetches corpus fixtures"
```

---

## Task 5: Standalone agent-backed bake-off runner

**Files:**
- Create: `source/server/cmd/compaction-bakeoff/main.go`

**Interfaces:**
- Consumes: `agentclient.Dial`/`StreamChat`, `BuildSummaryPrompt`/`ParseSummary`, `Corpus`, `Score`, all three contenders.
- Produces: a `main` that prints a metrics table. NOT a test (needs a live agent). Its only CI gate is that it compiles.

- [ ] **Step 1: Create `main.go`**

```go
// Command compaction-bakeoff scores the compaction contenders against a real
// model by routing summarization through a running cercano agent. It is a
// validation tool, not part of the test suite.
//
// Usage: compaction-bakeoff -addr localhost:50051
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/agentclient"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "running cercano agent gRPC address")
	flag.Parse()

	ctx := context.Background()
	client, err := agentclient.Dial(ctx, *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial agent at %s: %v\n", *addr, err)
		os.Exit(1)
	}
	defer client.Close()

	summarize := agentSummarizer(client)
	tok := contextmeter.Default()
	budget := compaction.Budget{VerbatimRecent: 4, SegmentTokens: 32000}

	contenders := []compaction.Compactor{
		compaction.RollingCompactor{},
		compaction.MapReduceCompactor{ModelReduce: false},
		compaction.MapReduceCompactor{ModelReduce: true},
	}

	fmt.Printf("%-22s %-18s %8s %10s %8s %6s %6s\n",
		"contender", "fixture", "reduce", "anchors", "dedup", "valid", "calls")
	invalid := false
	for _, c := range contenders {
		for _, f := range compaction.Corpus() {
			m, err := compaction.Score(ctx, c, f, summarize, tok, budget)
			if err != nil {
				fmt.Printf("%-22s %-18s  ERROR: %v\n", c.Name(), f.Name, err)
				continue
			}
			if !m.PairingValid {
				invalid = true
			}
			fmt.Printf("%-22s %-18s %7.0f%% %6d/%-3d %8d %6v %6d\n",
				c.Name(), f.Name, m.Reduction*100,
				m.AnchorsKept, m.AnchorsTotal, m.DedupCollapsed, m.PairingValid, m.ModelCalls)
		}
	}
	if invalid {
		fmt.Fprintln(os.Stderr, "FAIL: at least one send-view was pairing-invalid")
		os.Exit(1)
	}
}

// agentSummarizer builds a SummarizeFunc that sends the summary prompt through
// the agent and parses the streamed response.
func agentSummarizer(client *agentclient.Client) compaction.SummarizeFunc {
	return func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		prompt := compaction.BuildSummaryPrompt(msgs)
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		ch, err := client.StreamChat(cctx, "", prompt, "")
		if err != nil {
			return compaction.StructuredSummary{}, err
		}
		var out strings.Builder
		for m := range ch {
			switch m.Type {
			case agentclient.TypeToken:
				out.WriteString(m.Token)
			case agentclient.TypeDone:
				if m.Final != "" {
					out.Reset()
					out.WriteString(m.Final)
				}
			case agentclient.TypeError:
				return compaction.StructuredSummary{}, m.Err
			}
		}
		return compaction.ParseSummary(out.String()), nil
	}
}
```

- [ ] **Step 2: Verify it compiles + vets**

Run: `cd source/server && go build ./cmd/compaction-bakeoff/ && go vet ./cmd/compaction-bakeoff/`
Expected: builds clean (do NOT run it — it needs a live agent). If `TypeToken`/`TypeDone`/`TypeError` constant names differ in `agentclient`, grep `source/server/pkg/agentclient/` for the real `StreamMsgType` constants and use those.

- [ ] **Step 3: Full module build + suite**

Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS (the runner is excluded from tests; everything else green).

- [ ] **Step 4: Commit**

```bash
cd source/server
git add cmd/compaction-bakeoff/main.go
git commit -m "feat(server): standalone agent-backed compaction bake-off runner"
```

---

## Self-Review

**Spec coverage** (against `compaction-2a-algorithms-design.md`):
- §1 summarizer contract (prompt + lenient parser, missing→empty) → Task 1. ✓
- §2 agent-backed summarizer (Dial/StreamChat, accumulate, parse) → Task 5. ✓
- §3 contenders A/B/C over the shared pipeline (elide → split → summarize → assemble) → Tasks 2, 3. ✓
- §4 frozen-segment deferred → not implemented (correct). ✓
- §5 standalone runner, metrics table, pairing-invalid → non-zero exit → Task 5. ✓
- §6 corpus additions (long-debug, research-fetches) → Task 4. ✓
- §7 testing split: deterministic structure tests (fake summarizer) + parser tests in CI; quality run standalone → Tasks 1–4 tests, Task 5 compile-only. ✓

**Placeholder scan:** none. Task 2 Step 3 and Task 5 Step 2 carry explicit reconcile-against-real-names notes (the `llm` import; the `agentclient` `StreamMsgType` constant names) — guidance, not placeholders.

**Type consistency:** `SummarizeFunc`, `StructuredSummary`, `Budget`, `Result`, `Compactor` used with the part-1 signatures throughout. `segTokens(b)` defined in Task 2 (rolling.go) and reused in Task 3 (mapreduce.go) — same package, defined once. `splitRecent`/`renderSummaryMessages`/`BuildSummaryPrompt`/`ParseSummary` defined once in Task 1, consumed in Tasks 2/3/5. `recordingSummarizer` test helper defined once in Task 2's test, reused in Task 3's test (same package). The `flattenText` helper (part 1, metrics.go) is reused in Task 2's test.
