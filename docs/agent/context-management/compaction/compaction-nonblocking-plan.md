# Non-blocking Compaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A turn can never block on compaction, and compaction converges (bounded output, incremental catch-up) instead of growing or stalling.

**Architecture:** T1 makes the request path degrade mechanically and only *kick* the existing background generator. T2 makes `compactor.Advance` incremental (segment cap per pass + reschedule signal) and bounds the consolidated summary (re-consolidation fold). T3 adds pass logging + the summarizer warning and corrects the incident narrative in the design doc.

**Tech Stack:** Go 1.26, `cercano/source/server` only (packages `internal/compaction`, `internal/compactor`, `internal/compactiongen`, `internal/server`, `cmd/cercano`).

## Global Constraints

- **A turn never waits on a model call for compaction.** The request path's degrade steps are all LLM-free.
- **No new config knobs.** Bounds derive from existing `Config` fields (`SegmentTokens`, `ActivationFloorTokens`).
- **Pairing validity preserved:** any truncated view must not begin with orphaned `tool_result` content.
- **`CompactNow` remains** for explicit callers (command path, tests) but is no longer reachable from the turn path.
- Commit messages contain no "Claude"; no `Co-Authored-By`. gofmt-clean touched files; `go build ./...` + `go test ./... -count=1` green before every commit; `git status` clean after.

---

## File Structure

- T1: `internal/compaction/truncate.go` (new, + test), `internal/server/server.go` (`assembleHistory` ~2320, + test)
- T2: `internal/compactor/compactor.go` (`Advance` + consts, + tests), `internal/compactiongen/compactiongen.go` (reschedule-on-backlog)
- T3: `internal/compactiongen/compactiongen.go` (pass logging), `cmd/cercano/main.go` (summarizer warn), `docs/agent/context-management/compaction/compaction-nonblocking-design.md` (CompactedTokens correction)

---

### Task 1: LLM-free degrade on the request path

**Files:**
- Create: `source/server/internal/compaction/truncate.go`, `truncate_test.go`
- Modify: `source/server/internal/server/server.go` (`assembleHistory`, ~line 2320)
- Test: `source/server/internal/server/assemble_history_test.go` (create; mirror existing server-test harness)

**Interfaces:**
- Consumes: `agent.ScheduleCompaction(conversationID string)` (exists, nil-safe); `compaction.ElideSupersededToolResults`, `compaction.KeepLastNToolResults`, `compaction.TotalTokens`, `contextmeter.ModelMax/Default` (all exist).
- Produces: `func TruncateOldestToFit(msgs []llm.Message, tok contextmeter.Tokenizer, limit, preserveLeading int) ([]llm.Message, int)` — returns the fitted view and the number of messages dropped.

- [ ] **Step 1: Write the failing truncate tests** (`truncate_test.go`, package `compaction`; build `llm.Message` values directly — see `elide_*_test.go` in the package for message-construction style):

```go
func TestTruncateOldestToFit(t *testing.T) {
	tok := contextmeter.Default()
	mk := func(role llm.Role, text string) llm.Message {
		return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
	}
	big := strings.Repeat("x ", 4000) // ~4k tokens per message under the default tokenizer
	msgs := []llm.Message{
		mk(llm.RoleUser, big), mk(llm.RoleAssistant, big),
		mk(llm.RoleUser, big), mk(llm.RoleAssistant, big),
	}
	limit := compaction.TotalTokens(tok, msgs[2:]) + 10 // room for the last two only

	got, dropped := compaction.TruncateOldestToFit(msgs, tok, limit, 0)
	if dropped != 2 || len(got) != 2 {
		t.Fatalf("dropped=%d len=%d", dropped, len(got))
	}
	if compaction.TotalTokens(tok, got) > limit {
		t.Fatal("still over limit")
	}

	// preserveLeading=1 keeps the summary preamble even while dropping behind it.
	got2, _ := compaction.TruncateOldestToFit(msgs, tok, limit, 1)
	if got2[0].Blocks[0].Text != msgs[0].Blocks[0].Text {
		t.Fatal("leading message must be preserved")
	}

	// Already-fits input is returned unchanged.
	got3, dropped3 := compaction.TruncateOldestToFit(msgs, tok, 1<<30, 0)
	if dropped3 != 0 || len(got3) != len(msgs) {
		t.Fatal("must not touch a fitting view")
	}
}

func TestTruncateNeverLeadsWithToolResult(t *testing.T) {
	tok := contextmeter.Default()
	big := strings.Repeat("x ", 4000)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: big}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolName: "t", ToolInput: []byte(`{}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, Content: big}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "tail"}}},
	}
	// Force a limit that lands the cut between the tool_use and its result.
	limit := compaction.TotalTokens(tok, msgs[2:]) + 5
	got, _ := compaction.TruncateOldestToFit(msgs, tok, limit, 0)
	if len(got) > 0 {
		for _, b := range got[0].Blocks {
			if b.Type == llm.BlockToolResult {
				t.Fatal("truncated view must not begin with an orphaned tool_result")
			}
		}
	}
}
```

(Adapt the `llm.Block` field for tool-result content to the real struct — read `internal/llm/messages.go`; the assertions are the requirement.)

- [ ] **Step 2: Run; fail.** `cd source/server && go test ./internal/compaction/ -run Truncate -v` — undefined.

- [ ] **Step 3: Implement `truncate.go`:**

```go
package compaction

import (
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// TruncateOldestToFit drops whole messages from the front of msgs (after the
// first preserveLeading messages, which are kept unconditionally — the
// consolidated-summary preamble) until the total fits under limit. It never
// splits a message. After the size cut it keeps dropping while the first
// non-preserved message carries tool_result content, so the view never begins
// with an orphaned tool result. Returns the fitted view and how many messages
// were dropped. A view that already fits is returned unchanged.
func TruncateOldestToFit(msgs []llm.Message, tok contextmeter.Tokenizer, limit, preserveLeading int) ([]llm.Message, int) {
	if TotalTokens(tok, msgs) <= limit || len(msgs) == 0 {
		return msgs, 0
	}
	if preserveLeading > len(msgs) {
		preserveLeading = len(msgs)
	}
	head := msgs[:preserveLeading]
	tail := msgs[preserveLeading:]
	dropped := 0
	for len(tail) > 1 && TotalTokens(tok, append(append([]llm.Message{}, head...), tail...)) > limit {
		tail = tail[1:]
		dropped++
	}
	// Pairing validity: never lead with tool_result content.
	for len(tail) > 1 && hasToolResult(tail[0]) {
		tail = tail[1:]
		dropped++
	}
	return append(append([]llm.Message{}, head...), tail...), dropped
}

func hasToolResult(m llm.Message) bool {
	for _, b := range m.Blocks {
		if b.Type == llm.BlockToolResult {
			return true
		}
	}
	return false
}
```

(If recomputing `TotalTokens` per iteration is visibly slow in tests, subtract the dropped message's tokens incrementally — same semantics.)

- [ ] **Step 4: Run; pass.**

- [ ] **Step 5: Rework `assembleHistory`.** Replace the hard-override block in `server.go` (~2333-2342):

```go
	pct := compactionCfg.HardOverridePct
	if compactionCfg.Enabled && pct > 0 {
		hardLimit := int(float64(contextmeter.ModelMax(cloudModel)) * pct)
		if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
			// Never compact inline — kick the background generator (debounced,
			// deduped, timeout-bounded) and bring THIS turn under the limit with
			// LLM-free steps only.
			s.agent.ScheduleCompaction(convID)
			pre := compaction.TotalTokens(contextmeter.Default(), view)
			view, _ = compaction.ElideSupersededToolResults(view)
			if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
				view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
			}
			if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
				preserve := 0
				if state.ConsolidatedJSON != "" {
					preserve = 1 // keep the consolidated-summary preamble
				}
				var dropped int
				view, dropped = compaction.TruncateOldestToFit(view, contextmeter.Default(), hardLimit, preserve)
				fmt.Fprintf(os.Stderr, "[compaction] hard-override %s: %d tokens > limit %d — truncated %d oldest messages (background pass scheduled)\n",
					convID, pre, hardLimit, dropped)
			} else {
				fmt.Fprintf(os.Stderr, "[compaction] hard-override %s: %d tokens > limit %d — elision brought it under (background pass scheduled)\n",
					convID, pre, hardLimit)
			}
		}
	}
```

Note the existing `ElideToolResults`/`LossyToolElision` config blocks below stay as-is (idempotent re-application is documented in the code). `s.agent.CompactNow` is now unreferenced from this file — verify with grep that its only remaining callers are the explicit command path/tests.

- [ ] **Step 6: Write the failing server test** (`assemble_history_test.go`, mirror how existing server tests construct a `Server` with a store — read `server_test.go` for the harness; the watchdog tests also construct servers). Assertions:
  - Seed a conversation whose history exceeds a tiny hard limit (set `Compaction.Enabled=true, HardOverridePct` low with a small `ModelMax` via the cloud model name the meter maps — if forcing `ModelMax` is awkward, set `HardOverridePct` to a tiny fraction so the limit is small).
  - Call `s.assembleHistory(...)`: returned view fits under the limit; a spy/fake agent records `ScheduleCompaction` was called; no summarize/model call occurred (the fake store/agent has no model — reaching for one would panic, which is the guard).
  - If the agent seam makes spying hard, assert via the concrete `agent.Agent` with a nil compaction scheduler (ScheduleCompaction is nil-safe) and verify the view-fits + no-hang behavior; note in the report which shape was used.

- [ ] **Step 7: Verify + commit.** `go test ./internal/compaction/ ./internal/server/ -count=1` green; gofmt clean; `go build ./...` clean.

```bash
git -C <worktree> commit -am "fix(server): never compact inline — kick background pass and degrade the turn mechanically"
```

### Task 2: convergent compaction — segment cap per pass + bounded consolidated summary

**Files:**
- Modify: `source/server/internal/compactor/compactor.go` (`Advance`), `advance_test.go`
- Modify: `source/server/internal/compactiongen/compactiongen.go` (`runCompaction` reschedules on backlog)

**Interfaces:**
- Consumes: `compaction.SegmentByTokens`, `compaction.Reduce`, `compaction.AssembleSendView`, `compaction.TotalTokens` (exist).
- Produces: `Advance(...) (conversation.Compaction, changed bool, more bool, err error)` — **signature gains `more`** (backlog remains). Package consts `maxSegmentsPerPass = 4` and `reconsolidateThresholdSegments = 2`.

- [ ] **Step 1: Write the failing tests** in `advance_test.go` (mirror the existing tests' turn/state builders in that file — read it first; the assertions below are the requirement):

```go
func TestAdvanceCapsSegmentsPerPassAndSignalsMore(t *testing.T) {
	// Build a history whose eligible span yields > maxSegmentsPerPass segments.
	// With a stubbed summarize that counts invocations:
	//  - summarize is called at most maxSegmentsPerPass times
	//  - changed == true, more == true
	//  - FrozenThrough advanced to the end of the LAST PROCESSED segment (not
	//    the full eligible span)
	// A second Advance over the same turns + new state processes the next chunk;
	// repeated calls eventually yield more == false.
}

func TestAdvanceReconsolidatesWhenSummariesExceedBound(t *testing.T) {
	// Seed state whose parts list / consolidated summary renders to more than
	// reconsolidateThresholdSegments*SegmentTokens tokens (build fat
	// StructuredSummary parts). Stub summarize returns a small summary.
	// After Advance:
	//  - summarize was called one extra time with the consolidated content
	//  - SegmentSummariesJSON holds exactly ONE part (the re-consolidation)
	//  - the rendered consolidated view is under the bound
}

func TestAdvanceShrinkFailureSurfaces(t *testing.T) {
	// Same over-bound state, but the re-consolidation summarize returns an
	// error → Advance returns the error and the ORIGINAL state (no partial
	// persist of a grown state).
}
```

Write these as real tests against the file's existing helpers (turn builders, stub summarize funcs). The three behaviors are the contract; adapt construction details to the file's idiom.

- [ ] **Step 2: Run; fail** (signature + behavior). `go test ./internal/compactor/ -v` — compile error on the new `more` return is expected first.

- [ ] **Step 3: Implement in `compactor.go`:**

```go
// maxSegmentsPerPass caps how many segments one Advance call summarizes, so a
// large backlog (e.g. after days of failed passes) is digested incrementally —
// each pass fits comfortably inside the generator's runTimeout and progress is
// persisted between passes. The generator reschedules while more remains.
const maxSegmentsPerPass = 4

// reconsolidateThresholdSegments bounds the consolidated summary: when the
// consolidated view renders to more than this many segments' worth of tokens,
// the pass re-consolidates (summarizes the summaries) so compaction output
// shrinks instead of accumulating forever.
const reconsolidateThresholdSegments = 2
```

In `Advance`, after `segs := compaction.SegmentByTokens(elided, tok, cfg.SegmentTokens)`:

```go	segs := compaction.SegmentByTokens(elided, tok, cfg.SegmentTokens)
	more := false
	if len(segs) > maxSegmentsPerPass {
		segs = segs[:maxSegmentsPerPass]
		more = true
	}
```

Summarize the capped `segs` as today. **FrozenThrough must advance only through the last processed segment**: track the last turn covered by the capped segments. `SegmentByTokens` returns segments of *messages*, not turns — derive the boundary by counting how many eligible messages the capped segments cover and mapping back to the corresponding turn's `CreatedAt` (the existing message-per-turn relationship comes from `agent.BuildLLMHistory(eligible)`; if one turn yields multiple messages, advance only through the last turn whose messages are FULLY covered). If the mapping is genuinely 1-turn↔1-message in `BuildLLMHistory`, this is an index; verify by reading `BuildLLMHistory` and state the finding in the report.

After `consolidated := compaction.Reduce(parts)`, add the bound:

```go
	bound := reconsolidateThresholdSegments * cfg.SegmentTokens
	if TotalTokensOfSummary(tok, consolidated) > bound {
		re, err := summarize(ctx, compaction.AssembleSendView(consolidated, nil))
		if err != nil {
			return state, false, false, err // never persist a grown state on failure
		}
		parts = []compaction.StructuredSummary{re}
		consolidated = re
	}
```

where `TotalTokensOfSummary` renders the summary the same way the send view does (`compaction.TotalTokens(tok, compaction.AssembleSendView(sum, nil))`) — inline it or add the tiny helper, whichever reads better.

All `return` statements gain the `more` value (`false` on the gate returns; the computed `more` on success).

- [ ] **Step 4: Update the callers.** `compactiongen.runCompaction`: adapt to the new signature; after a successful save, if `more` is true, call `g.Schedule(conversationID)` so the next chunk runs after the debounce (comment: backlog converges one bounded pass at a time). Fix any other `Advance` callers/tests the compiler reports.

- [ ] **Step 5: Run; pass.** `go test ./internal/compactor/ ./internal/compactiongen/ -count=1 -v`.

- [ ] **Step 6: Verify + commit.** Full `go test ./... -count=1` in `source/server`; gofmt; build.

```bash
git -C <worktree> commit -am "fix(compactor): cap segments per pass with reschedule-on-backlog; bound the consolidated summary"
```

### Task 3: observability + doc corrections

**Files:**
- Modify: `source/server/internal/compactiongen/compactiongen.go` (`runCompaction`)
- Modify: `source/server/cmd/cercano/main.go` (~line 200, generator wiring)
- Modify: `docs/agent/context-management/compaction/compaction-nonblocking-design.md`
- Test: `source/server/internal/compactiongen/compactiongen_test.go`

- [ ] **Step 1: Pass logging.** In `runCompaction`, wrap the pass:

```go
	start := time.Now()
	pre := /* TotalTokens of the pre-pass send view — compute from turns+state via compactor.BuildSendView */
	fmt.Fprintf(os.Stderr, "[compaction] pass start %s: %d tokens\n", conversationID, pre)
	// ... existing Advance + save ...
	// on success:
	fmt.Fprintf(os.Stderr, "[compaction] pass ok %s: %d -> %d tokens in %s (more=%v)\n", conversationID, pre, post, time.Since(start).Round(time.Millisecond), more)
	// on error:
	fmt.Fprintf(os.Stderr, "[compaction] pass FAILED %s after %s: %v\n", conversationID, time.Since(start).Round(time.Millisecond), err)
```

(Read `runCompaction` first and place these around its real structure; `post` = TotalTokens of the post-save send view. Errors were previously swallowed silently — they must now always log.)

- [ ] **Step 2: Test the logging contract.** In `compactiongen_test.go`, drive a pass with a stubbed summarize that (a) succeeds and (b) fails; capture stderr (or refactor the generator to take an optional `logf func(format string, args ...any)` defaulting to stderr — cleaner to test; implementer's choice, note it in the report) and assert a `pass ok` line and a `pass FAILED` line respectively.

- [ ] **Step 3: Summarizer warning.** In `main.go` where `compGen` is constructed (~line 216), before `compactiongen.New`:

```go
		if cfg.Compaction.SummarizerModel == "" {
			fmt.Fprintf(os.Stderr, "[compaction] WARN: no summarizer_model configured — compaction summarizes with the interactive local model (%s), which can be very slow for large histories\n", cfg.LocalModel)
		}
```

- [ ] **Step 4: Design-doc correction.** In `compaction-nonblocking-design.md`, fix the incident narrative in point 2: `compacted_tokens = 338,394` is the **cumulative digested-token counter**, not the view size. Replace the sentence with the corrected growth analysis: (a) the un-frozen live tail grew for two days after passes stopped succeeding; (b) catch-up passes covering the whole backlog exceeded the generator timeout and never completed (all-or-nothing `Advance`); (c) `Reduce` is a mechanical merge whose output grows with the segment count. Note that §2's fix maps to (b) the segment cap and (c) the re-consolidation bound.

- [ ] **Step 5: Verify + commit.** `go test ./internal/compactiongen/ -count=1` + full module; gofmt; build.

```bash
git -C <worktree> commit -am "feat(compaction): pass start/ok/FAILED logging, summarizer warning, incident-narrative correction"
```

---

## Self-Review

- **Spec coverage:** §1 never-block (T1: Schedule kick + elide→keepN→truncate + breach logs; CompactNow unreferenced from the turn path). §2 bounded/convergent (T2: segment cap + more/reschedule + re-consolidation bound + shrink-failure surfaces). §3 observability (T3 logging + warn). §4 out-of-scope respected (no recap changes, no meter changes, no new knobs). Design-doc correction added (T3 S4) for my CompactedTokens misread.
- **Placeholder scan:** T2 S1 gives contracts-as-comments rather than full test code — deliberate: the test construction must reuse `advance_test.go`'s existing builders (unknown here); the three behavioral contracts are fully specified. T1 S6 similarly binds to the real server-test harness with explicit assertions. Both say exactly what to read. No TBDs.
- **Type consistency:** `TruncateOldestToFit(msgs, tok, limit, preserveLeading) ([]llm.Message, int)` defined T1 S3, used T1 S5, tested T1 S1. `Advance` 4-return (`state, changed, more, err`) defined T2 S3, consumed T2 S4. Consts `maxSegmentsPerPass`/`reconsolidateThresholdSegments` defined and used in T2 only.
- **Risk notes:** T2's FrozenThrough-boundary mapping (segments→turns) is the one subtle spot — the plan requires the implementer to verify the `BuildLLMHistory` turn↔message relationship and report it; the existing same-second boundary-trim logic in `Advance` must be preserved for the capped boundary too (it operates on `eligible` before segmentation, so capping segments must re-apply the same-second rule at the new boundary — implementer must check this interaction and cover it in the capped-pass test).
