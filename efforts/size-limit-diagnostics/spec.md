# Spec: Legible size-limit diagnostics for the tool loop

## Problem

Two distinct "payload too big" failures in the agentic tool loop both surface as
misleading, opaque errors, causing wasted retries and dead-end loops.

### Failure A — output truncation misreported as malformed JSON (primary)

When the model emits a large tool-call argument (e.g. a `Write` with a big file
body), generation hits the per-turn output cap and the JSON arguments are cut off
mid-object. The provider reports this via the finish/stop reason
(`length` for OpenAI/llama-server, `max_tokens` for Anthropic), which every
adapter already captures (`openai/stream.go:109`, anthropic `stop_reason`,
ollama). But the tool loop ignores it: at `toolloop.go:453` it detects the
truncated JSON via `llm.MalformedToolInput(...)` and returns
`"tool input was not valid JSON — resend the call with arguments as a single
valid JSON object."` — advice that is actively wrong for a truncation. The model
resends the identical oversized call and truncates again.

**Observed live** in conversation `eacdedec3613124d` (CERCANO - MCP): three
consecutive `Write` calls to `mcp_dashboard.go`, each returning the same
`Raw input received: {"path": "...mcp_dashboard.go"` fragment (cut off before
`content`). The model self-diagnosed ("the large content is likely tripping the
tool") but the loop's feedback pushed retry instead of chunking.

**Root cause (verified):** the tool loop hard-defaults the output budget to
`maxTokens = 4096` (`toolloop.go:305`), forwarded as `num_predict = 4096` to
ollama/llama-server (`ollama/client.go:58-60`). 4096 tokens ≈ 12–16 KB — smaller
than a real source file, so large `Write` args truncate. The finish reason that
would explain this is discarded.

### Failure B — input context overflow, opaque passthrough (issue #29, related)

The dispatch/local tier runs a small-window `llama_server` model
(`context_size` default 8192, `config.go:532`). A large task payload overflows
the *input* window before any tool runs. The llama-server path returns a usable
`request (N tokens) exceeds the available context size (M tokens)`, but the
OpenAI-compatible cloud path returns the opaque
`openai unknown: error, Context size has been exceeded.` Reproduced live during
this effort's own recon dispatch.

## Goals

1. When a tool call is truncated at the output cap, tell the model the truth:
   the call was cut off because it was too large; split it into smaller
   Write/Edit calls. Never emit "invalid JSON" for a truncation.
2. Give the tool loop enough default output budget that ordinary source-file
   writes do not truncate.
3. Normalize both size failures (input overflow, output truncation) into named
   `llm.Error` classes carrying counts where the provider supplies them, so both
   the local and cloud paths produce actionable, uniform diagnostics.

## Non-goals

- Do NOT attempt to auto-repair or reconstruct truncated JSON. The missing bytes
  were never generated; the correct response is honest diagnosis + model retry
  (chunked).
- Font picker, `/diff` renderer, Homebrew formula, and other CLI-surface items
  are out of scope.
- Raising `llama_server.context_size` for the dispatch tier is tracked under
  issue #29; this effort only normalizes the *error* on that path (goal 3), not
  the config bump.

## Acceptance criteria

- A `Write` tool call truncated at the output cap yields a tool-result that names
  truncation and instructs chunking — verified by a unit test feeding a
  truncated tool-call block with `StopReason: "length"`.
- The tool loop's default per-turn output budget is raised from 4096 to a value
  that comfortably fits a typical source file (see plan), with the value sourced
  from config rather than a bare literal.
- A truncated call that is NOT at the output cap still falls through to the
  existing malformed-JSON path (no regression) — covered by a test with a
  genuinely malformed input and `StopReason: "stop"`.
- Both `llama_server` and OpenAI-compatible input-overflow errors normalize to a
  single `context_overflow` `llm.Error` class; a unit test asserts the mapping
  for each adapter's raw string.
- `go build ./...` and `go test ./...` pass for the server module.
