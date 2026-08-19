# Spec: Bound tool-result payloads

## Problem

A single tool result can exceed any local context window in one step, bricking
the caller irrecoverably.

Observed in `CERCANO - DUPLICATE LLAMA-SERVER`, sub-agent `5468a35ac91ab7bfc9c81b73`:

```text
iter=1: messages=2  message_bytes=1679   approx_prompt_tokens=853    status=200
iter=2: estimated_tokens=94632  tool_tokens=420  messages=3  → preflight context_overflow
```

The sub-agent was correctly isolated — its first request was 853 tokens, two
messages, `lean_subagent_prompt=true`. It received no parent history. It then
made one `Grep` call and could never make another.

Turn sizes from the conversation store:

```text
71098 user          609 chars  ~   152 tok   the task
71099 assistant     354 chars  ~    88 tok   reasoning + Grep call
71100 user      360,535 chars  ~90,133 tok   the Grep result
```

99.7% of the sub-agent's context was one tool result. The triggering call:

```json
{"pattern":"llama.*server.*spawn|llama.*server.*start|spawn.*llama|start.*llama"}
```

No `path`, no `glob` — a four-way alternation across the whole repository,
including `.claude/worktrees/`.

## Root cause

Truncation policy exists but is incomplete. `NewRowsResult` bounds row *count*
but not row *size*:

```go
const maxRows = 200
if len(rows) > maxRows { ... }   // 107 rows → never fires
r.Rows = rows                    // 346,048 chars pass through untouched
```

The result had 107 rows (median 539 chars) but 12 rows over 10 KB, the largest
20,501 chars — a single minified line from
`internal/compaction/testdata/real_conversation.json`.

The asymmetry between the two constructors is the defect:

```text
NewTextResult :  32 KiB cap   ≈  8,192 tok
NewRowsResult : 200 rows, UNBOUNDED bytes
incident      : 107 rows     ≈ 86,512 tok  (264% of a 32k window)
worst case    : 200 × 20 KB  ≈  1,000,000 tok through a "capped" path
```

This is unrecoverable rather than merely wasteful: history trimming preserves
the newest messages, and the oversized payload *is* the newest message. The
trimmer has nothing legal to drop.

Two further findings:

- The policy is **duplicated**: `capabilities/capability.go` and
  `agenttools/tool.go` each define their own `maxRows = 200` / `maxBytes = 32 * 1024`.
  Any fix must land in both, or the duplication must be removed.
- MCP results (`mcp_host/tool.go`) route through `NewTextResult`, so they are
  byte-capped today. The gap is specific to rows.

## Goals

1. No single tool result can exceed a bounded share of the caller's context window.
2. Row results honour a byte ceiling as well as a row ceiling, trimming whole
   rows and over-long row values so output stays structurally valid JSON.
3. Truncation is always visible to the model, with actionable guidance naming
   the remedy (`path` / `glob`).
4. Callers with a known context window get a window-proportional ceiling;
   callers without one (MCP, external hosts) get a fixed structural ceiling.
5. Forward progress is preserved — a broad query degrades to a partial answer,
   not a failed iteration.

## Non-goals

- Do not change history trimming or compaction. They cannot help here.
- Do not reject broad queries outright (see Decision 3).
- Do not plumb a context window into `NewRowsResult` / `NewTextResult`; they are
  context-free constructors called from capabilities, `agenttools`, and MCP.
- Do not alter the existing 200-row or 32 KiB values for text; this is about
  closing the rows-byte gap and adding a window-aware backstop.

## Decisions

Three forks were resolved with the user during planning.

### Decision 1 — Enforcement placement: **both layers**

Constructor and tool loop do different jobs:

- `NewRowsResult` enforces an absolute structural ceiling. It can trim whole
  rows and oversized row values while keeping valid JSON, which the loop cannot
  do because it only sees rendered text.
- The tool loop enforces a window-proportional ceiling as a last-resort guard,
  catching non-row and future/MCP bloat the constructors cannot see.

Rejected: constructor-only leaves a hole for any future non-row producer.
Loop-only would cut rendered JSON mid-structure, handing the model malformed
input — strictly worse than a clean row-level trim.

### Decision 2 — Cap policy: **fixed at constructor, scaled at loop**

Grounding:

```text
32 KiB ≈ 8,192 tok  →  25% of a 32k local window
                    →   4% of a 200k cloud window
```

A fixed cap alone is simultaneously too loose for sub-agents (three such
results still brick a 32k window) and too tight for cloud callers.

Scaling belongs in the loop because that is the only layer that knows the
window: `in.ContextWindow` is already in scope where `res.LLMContent()` is
called, and results converge through `toolResultBlocks`.

Scaling was considered at the constructor (passing a budget via the existing
`capabilities.Call` struct). Rejected because `NewRowsResult` does not receive
`Call`, and because the MCP surface has no caller model at all — it would still
need a fixed default. That variant converges on this design with a larger blast
radius.

### Decision 3 — Overflow behaviour: **truncate and tell**

Matches the established contract: `Result.Truncated` and `Result.Note` already
exist, and `LLMContent()` already appends the note as `(…)`.

Rejected: rejecting the call costs a full iteration, which the smallest-window
callers can least afford — they are precisely the ones failing today.

Accepted risk: a model may treat a truncated search as complete. Mitigated by
making the note explicit about what was dropped and how to get the rest.

## Design

### Row byte ceiling

`NewRowsResult` gains a byte ceiling alongside the row ceiling:

1. Trim any individual row *value* exceeding a per-value limit, marking the
   elision inline so the row stays valid and readable.
2. Accumulate rows until the serialized budget is reached, then stop and drop
   the remainder.
3. Set `Truncated` and write a `Note` reporting both dimensions when they apply,
   e.g. `showed 40 of 107 matches (32 KiB cap); narrow with path/glob for the rest`.

Row order is preserved; no reordering or prioritisation.

### Window-proportional loop guard

At the tool-loop seam where `res.LLMContent()` becomes a `BlockToolResult`:

1. Measure the rendered content.
2. If `in.ContextWindow > 0` and the content exceeds the configured share of it,
   truncate rune-safely and append a note in the same style.
3. If `in.ContextWindow == 0` (unknown window), leave the constructor ceiling as
   the only bound.

The guard applies to every tool result regardless of producer, including MCP.

### Note wording

Notes must state what was dropped, the limit that applied, and the remedy.
The incident query had neither `path` nor `glob`, so naming those is the
highest-value part of the message.

### Duplication

`capabilities/capability.go` and `agenttools/tool.go` hold identical copies of
the policy. The fix must either land in both or consolidate them into one
shared implementation. Consolidation is preferred if it does not create an
import cycle; this must be checked during implementation, not assumed.

## Acceptance criteria

- A row result of 107 rows totalling ~346 KB is reduced to at or under the byte
  ceiling, with `Truncated` set and a note naming the counts and the remedy.
- A single 20,501-char row value is trimmed without corrupting the row.
- Row results under both ceilings are returned byte-identical to today.
- The 200-row cap still fires for many small rows, with its existing note.
- Text results are unchanged.
- With a 32k window, no single tool result exceeds the configured share of it.
- With an unknown window (0), the constructor ceiling still applies and nothing
  panics.
- Reconstructing the incident's sub-agent shape yields a second iteration that
  fits, rather than `preflight context_overflow`.
- Both duplicated policy sites behave identically, or are consolidated.
- Truncated row output remains valid JSON.
- Full server suite passes.

## Verification notes

The incident is reproducible from stored state: conversation
`5468a35ac91ab7bfc9c81b73`, turn rowid `71100`, is the 360,535-char result. A
regression test should use a synthetic fixture of the same shape (roughly 107
rows, one ~20 KB value) rather than depending on the live database.

New tests must be confirmed to fail against current `main` before the fix, as
was done for the context-window regression tests in `bc2491f6cc5a`.
