# Plan: Bound tool-result payloads

Effort: `efforts/cap-tool-result-payloads`
Spec: `efforts/cap-tool-result-payloads/spec.md`

## Phase 0 — Confirm the shape before changing anything

- [ ] Check whether `capabilities/capability.go` and `agenttools/tool.go` can
      share one implementation without creating an import cycle. Record the
      answer; it decides Phase 1's shape.
- [ ] Confirm every `NewRowsResult` caller tolerates a shorter row slice
      (grep, git_read, fs_read, and any others found).
- [ ] Confirm `in.ContextWindow` is reliably populated at the `toolResultBlocks`
      seam for both main-thread and sub-agent runs; note what it holds when the
      window is unknown.

## Phase 1 — Row byte ceiling in the constructor

- [ ] Add a per-value trim so a single oversized row value cannot dominate a
      result, keeping the row structurally valid.
- [ ] Add a serialized-byte ceiling across rows, accumulating in order and
      dropping the remainder once the budget is reached.
- [ ] Preserve row order; no reordering or prioritisation.
- [ ] Set `Truncated` and write a `Note` covering both dimensions when they
      apply, naming counts, the limit, and the `path`/`glob` remedy.
- [ ] Keep the existing 200-row behaviour and its note intact for the
      many-small-rows case.
- [ ] Apply to both policy sites, or consolidate per Phase 0.

## Phase 2 — Tests for the constructor

- [ ] Synthetic fixture matching the incident: ~107 rows, one ~20 KB value,
      ~346 KB total. Assert the result lands at or under the ceiling, is
      `Truncated`, and the note names counts and remedy.
- [ ] A single oversized row value is trimmed and the row remains valid.
- [ ] Results under both ceilings are byte-identical to today (no regression
      for the common case).
- [ ] The 200-row cap still fires for many small rows.
- [ ] Truncated row output still parses as valid JSON.
- [ ] Confirm these tests fail against current `main` before the fix.

## Phase 3 — Window-proportional loop guard

- [ ] At the seam where `res.LLMContent()` becomes a `BlockToolResult`, measure
      rendered content and apply a ceiling proportional to `in.ContextWindow`.
- [ ] Truncate rune-safely and append a note in the same style as the
      constructor's.
- [ ] When `in.ContextWindow == 0`, apply no scaling and leave the constructor
      ceiling as the only bound.
- [ ] Ensure the guard covers every tool result, including MCP-sourced ones.
- [ ] Make the chosen fraction a named constant with a comment explaining the
      choice, not a bare literal.

## Phase 4 — Tests for the loop guard

- [ ] With a 32k window, an oversized result is capped to the configured share.
- [ ] With window 0, behaviour falls back to the constructor ceiling and does
      not panic.
- [ ] A normal small result passes through untouched.
- [ ] Reconstruct the incident's sub-agent shape (853-token iter 1, then a large
      row result) and assert iteration 2 fits rather than raising
      `preflight context_overflow`.
- [ ] Confirm the incident test fails against current `main`.

## Phase 5 — Verification

- [ ] `go test ./internal/capabilities/... ./internal/agenttools/... ./internal/agent/...`
- [ ] `go test ./...` (full server suite).
- [ ] Re-read the notes as the model would see them via `LLMContent()`; confirm
      they are actionable and name `path`/`glob`.
- [ ] Checkpoint with a commit body stating the measured before/after for the
      incident shape.

## Phase 6 — Deploy

- [ ] Rebuild `~/bin/.cercano-libexec/cercano`.
- [ ] Flag to the user that a restart is required for the fix to take effect,
      and let them choose when — a restart severs in-flight conversations.

## Notes

- Two prior fixes in this area were verified correct but were not running,
  because the binary predated them. Phase 6 is not optional bookkeeping.
- The trimmer cannot mitigate this class of failure: the oversized payload is
  the newest message, which trimming must preserve. The cap is the only defence.
