# Plan: Full Request Context Meter

## Phase 1 — Model window certainty

- [x] Update context-window helpers in `source/server/internal/contextmeter` to return both a window value and whether the value is known.
- [x] Preserve current known Claude behavior at 200,000 tokens.
- [x] Ensure unknown OpenAI names such as `gpt-5.5` can still receive a default operational window but are marked unknown/defaulted.
- [x] Add or update contextmeter tests for known Claude, unknown `gpt-5.5`, and existing fallback behavior.
  Verification:
- [x] Run focused contextmeter tests.

## Phase 2 — Full request accounting data model

- [x] Extend request/context accounting structs near `source/server/internal/requestassembly` and `source/server/internal/modelbudget` as needed with explicit fields:
  - message tokens
                                                                                    - raw tokens
                                                                                    - system tokens
                                                                                    - tool schema tokens
                                                                                    - output reserve tokens
                                                                                    - estimated total request tokens
                                                                                    - context window
                                                                                    - context window known/defaulted flag
- [x] Keep existing message/raw token fields backward-compatible.
- [x] Add helper logic to estimate system and tool schema tokens from the `llm.ChatRequest`-level components available before provider dispatch.
- [x] Keep estimates provider-agnostic and clearly named as estimates.
  Verification:
- [x] Add focused unit tests for request accounting sum behavior.
- [x] Run focused requestassembly/modelbudget tests.

## Phase 3 — Context usage RPC/client surface

- [x] Extend `source/proto/agent.proto` `GetContextUsageResponse` with additive fields for full request accounting and window certainty.
- [x] Regenerate protobuf bindings.
- [x] Update server-side `GetContextUsage` implementation in host persistence to populate the new fields.
- [x] Update `source/server/pkg/agentclient` context usage mapping/types.
- [x] Preserve compatibility when older/zero fields are absent.
  Verification:
- [x] Run focused server tests that cover `GetContextUsage`.
- [x] Run proto/build checks for server and CLI modules.

## Phase 4 — CLI context meter display

- [x] Update CLI UI state structs to store the new context usage fields.
- [x] Change the primary percentage/bar to use estimated full request tokens when present.
- [x] Continue showing compacted/message tokens as detail.
- [x] Display an uncertainty label when the context window is defaulted/estimated rather than known.
- [x] Keep fallback formatting for old responses that only include `tokens_used`, `raw_tokens`, and `model_max`.
  Verification:
- [x] Add/update CLI formatting tests for:
  - full request accounting present;
                                                                                    - known window;
                                                                                    - defaulted/estimated window;
                                                                                    - legacy response fields only.
- [x] Run focused CLI tests.

## Phase 5 — Diagnostics and failure visibility

- [x] Add full request accounting fields to route/context diagnostic logs where `context.assembled`, `loop.start`, or equivalent events are emitted.
- [x] Include provider-reported `InputTokens`/`OutputTokens` in result/failure diagnostics where already available without inventing values when absent.
- [x] Ensure logs avoid prompt text, tool arguments, API keys, and response bodies.
  Verification:
- [x] Add/update focused tests for route/context logging if existing tests cover those events.
- [x] Manually inspect a local context usage response/log line in a safe way if practical.

## Phase 6 — Integrated verification and checkpoint

- [x] Run focused Go tests for changed server packages.
- [x] Run focused Go tests for changed CLI packages.
- [x] Run formatting/code generation checks required by the changed files.
- [x] Summarize behavior and any remaining uncertainty around exact OpenAI model windows.
- [x] Checkpoint the completed changes with a conventional commit.
