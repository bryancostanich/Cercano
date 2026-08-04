# Plan: Legible size-limit diagnostics for the tool loop

Effort: `efforts/size-limit-diagnostics`
Spec: `efforts/size-limit-diagnostics/spec.md`

## Phase 1 — Diagnose output truncation correctly (fixes the live hang)

- [x] Add a stop-reason classifier in `internal/llm` (e.g. `IsLengthTruncation(stopReason string) bool`) recognizing `"length"` (OpenAI/llama-server) and `"max_tokens"` (Anthropic). Unit-test the mapping including the empty/`"stop"`/`"tool_use"` negatives.
- [x] Thread `resp.StopReason` into the malformed-input branch of the tool loop (`toolloop.go:453`). Before returning the "invalid JSON" result, if the response was length-truncated AND the offending tool input parses as truncated, return instead: a tool-result that states the call was cut off at the output-token limit and instructs the model to split the file into multiple smaller Write/Edit calls.
- [x] Keep the existing malformed-JSON message as the fallback for genuinely malformed input that is NOT length-truncated (no regression).
- [x] Tests: (a) truncated `Write` arg + `StopReason:"length"` → chunking guidance; (b) malformed arg + `StopReason:"stop"` → existing invalid-JSON message.

## Phase 2 — Raise the default output budget

- [x] Replace the bare `maxTokens := 4096` literal (`toolloop.go:305`) with a config-sourced default. Add a config field (e.g. `ToolLoop.MaxTokensPerTurn` or reuse existing config plumbing) defaulting to a value that fits a typical source file (proposed 8192; justify in the commit body against the 4096≈12–16KB finding).
- [x] Verify `MaxTokensPerTurn` override still wins when set (`toolloop.go:306-307`).
- [x] Test: default flows through to `llm.ChatRequest.MaxTokens` when unset; override respected when set.

## Phase 3 — Normalize size errors into named llm.Error classes

- [x] Define `context_overflow` (input) and `output_truncated` (output) error kinds in the `internal/llm` error taxonomy, carrying `used`/`limit` fields when the provider supplies them.
  <!-- Local surprise (execution): output truncation is NOT an error — it
                      surfaces as a successful ChatResponse with StopReason "length", already made
                      legible by Phase 1's IsLengthTruncation. An `output_truncated` ErrorClass
                      would be dead code (nothing constructs it), so it is dropped. Only the
                      input-side `context_overflow` class is added. -->
- [x] Map the llama-server `request (N tokens) exceeds the available context size (M tokens)` string to `context_overflow` with parsed counts.
- [x] Map the OpenAI-compatible `Context size has been exceeded` passthrough to `context_overflow` (counts absent → zero/unknown).
- [x] Tests: adapter-level string → normalized error class + parsed counts for each path.

## Verification gate

- [x] `go build ./...` (server module)
- [x] `go test ./...` (server module) — focus `internal/agent`, `internal/llm`
- [x] Re-run the MCP dashboard scenario shape as a loop-level test if feasible (truncated large Write → chunking guidance, not infinite retry).

## Sequencing notes

- Phase 1 alone stops the observed infinite retry — ship-critical, smallest surface.
- Phases 1 + 3 touch the same two areas (`toolloop.go`, `internal/llm`), so land them together to avoid a second pass; Phase 2 is a small config follow-on that can ride in the same PR.
- Issue #29's `context_size` bump for the dispatch tier is a separate config change; this effort makes its error legible (Phase 3) but does not change the value.
