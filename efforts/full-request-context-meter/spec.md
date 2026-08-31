# Full Request Context Meter

## Problem

The current context meter and context usage RPC report the compacted conversation/message send-view. That number is useful, but it is not the full request sent to a provider. A provider request also includes system/developer prompt text, native tool schemas, MCP tool schemas, output reserve/max token budget, provider wire-format overhead, and provider-specific tokenization differences.

This caused a confusing failure mode: the UI could show roughly 95k compacted context tokens while OpenAI Responses rejected the request with `context_overflow`, and nearby provider usage logs reported much larger `tokens_in` values. The meter was not lying about the message send-view; it was showing the wrong denominator for user-facing capacity risk.

The current model-window table also silently defaults unknown models, including `gpt-5.5`, to 128k. That default is useful as an operational fallback, but the UI should not present it as a known provider context window.

## Goals

- Extend context usage accounting to include an estimated full request budget:
  - message/send-view tokens
  - system prompt tokens
  - tool schema tokens
  - output reserve/max token budget
  - total estimated request tokens
  - context window value used for budgeting
  - whether that context window is known or defaulted
- Keep the existing compacted/raw message accounting visible and backward-compatible.
- Use the full estimated request total for the primary CLI context meter percentage when available.
- Label uncertain/defaulted context windows honestly so unknown models do not look like known provider limits.
- Add route/request logging fields that make future context-overflow failures diagnosable without reconstructing state from multiple logs.
- Preserve provider-reported usage (`InputTokens`/`OutputTokens`) as post-hoc evidence where already available, but do not rely on it for preflight/metering.

## Non-goals

- Do not implement exact provider tokenizers for OpenAI or Anthropic in this effort.
- Do not rewrite provider adapters to expose exact wire-token counts.
- Do not change compaction policy beyond using/displaying better accounting.
- Do not hard-code guessed OpenAI `gpt-5.5` windows unless an authoritative source is available in code/config.
- Do not reduce the tool catalog or introduce dynamic tool-schema pruning in this effort.

## Design direction

### Request accounting

Add explicit request-accounting fields near the existing request assembly/context usage path rather than inside individual provider adapters. The estimate should be computed from the resolved `llm.ChatRequest`-level components used for the next turn:

- `message_tokens`: existing assembled send-view token count.
- `raw_tokens`: existing raw conversation token count.
- `system_tokens`: tokenizer count for the system prompt text used by the tool loop.
- `tool_schema_tokens`: tokenizer estimate for serialized tool definitions available on that turn.
- `output_reserve_tokens`: requested/max output reserve included in the budget.
- `estimated_request_tokens`: sum of message/system/tool/output reserve plus any simple explicit overhead the implementation chooses to track.
- `context_window`: model context window used for the budget.
- `context_window_known`: true only when the window came from a known provider/model entry or explicit runtime config; false when defaulted.

The estimate must be labeled as estimated because provider-side tokenization and provider wire formats can differ.

### Window certainty

Update context-window APIs so callers can distinguish:

- known model/provider windows,
- explicit runtime-config windows,
- default fallback windows.

For Anthropic, the existing generic Claude window is 200,000 tokens and should remain known. For OpenAI `gpt-5.5`, current local evidence is insufficient to assert the exact window; it should be shown as a defaulted/estimated window unless an authoritative value is added.

### CLI display

The CLI should keep compacted/raw details but make the main percentage reflect full estimated request usage when fields are available. A concise display could read like:

```text
Context: 112k / 128k est request (95k messages, 12k tools, 3k system, 2k output) · window estimated
```

Exact wording can be adjusted during implementation, but the UI must no longer imply that compacted message tokens alone are the provider request size.

### Logging

Route/request diagnostics should include the same full accounting fields so a future `context_overflow` log contains enough data to compare:

- compacted message tokens,
- estimated total request tokens,
- provider-reported input tokens when available,
- window value and known/defaulted status.

## Acceptance criteria

- `GetContextUsage` returns explicit full-request accounting fields while preserving existing fields.
- The CLI context meter uses estimated full request tokens for the primary percentage when present.
- The CLI still exposes compacted/message tokens so users can understand what compaction did.
- Unknown/defaulted model windows are distinguishable from known windows in server structs/RPC/client/UI.
- Tests cover:
  - full estimate includes messages, system, tools, and output reserve;
  - known Claude window remains known at 200k;
  - unknown `gpt-5.5` does not silently appear as a known 128k window;
  - CLI formatting shows estimated full request accounting without breaking old/zero-field responses;
  - route/context logs include the new fields where practical.
