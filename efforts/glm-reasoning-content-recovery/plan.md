# GLM Reasoning Content Recovery

Recover the visible answer when an OpenAI-compatible provider returns empty `content` but non-empty plaintext `reasoning_content`, in both non-streaming and streaming paths. Then verify against GLM-4.5-Air and update the catalog/config so GLM is no longer left marked or routed as broken.

Anchor: `efforts/glm-reasoning-content-recovery/spec.md`. Decisions 1–4 there are load-bearing.

## Phase 1 — Non-streaming adapter fallback

Objective: in the completed-message mapper, promote `reasoning_content` to a visible text block only when `content` is empty.
Files: `source/server/internal/llm/openai/adapter.go`, `source/server/internal/llm/openai/adapter_test.go`.
Tests: content-empty + reasoning populated → one text block from reasoning; content present + reasoning present → text block from content only (reasoning ignored); both empty → no text block; tool-call-only message unaffected; existing qwen double-emit suppression still holds.

- [x] In `blocksFromOpenAI`, after the existing `m.Content` text-block logic, add: if no text block was produced (content empty or suppressed) and `m.ReasoningContent != ""`, append a `BlockText` whose `Text` is `m.ReasoningContent`
  - [x] Ensure the fallback does not fire when `m.Content` produced a text block
  - [x] Ensure the fallback does not fire when content was suppressed as a duplicate tool-call JSON array (do not resurrect suppressed noise via reasoning)
- [x] Add table-driven tests in `adapter_test.go` covering the four cases above plus tool-call coexistence
- [x] Run `go test ./internal/llm/openai/...` from `source/server`

## Phase 2 — Streaming adapter fallback

Objective: buffer `reasoning_content` deltas and emit them as visible text at stream end only if no normal text delta was emitted.
Files: `source/server/internal/llm/openai/stream.go`, `source/server/internal/llm/openai/stream_test.go`.
Tests: reasoning-only stream → single terminal text delta from buffered reasoning before `EventMessageStop`; stream with normal content → reasoning buffer discarded, no duplicate text; interleaved content+reasoning → only normal content shown; tool-call-only stream unaffected.

- [x] Add `reasoningBuf strings.Builder` (or string) and `emittedText bool` fields to `streamReader`
- [x] In the delta handling, set `emittedText = true` whenever a non-empty `delta.Content` text delta is emitted
- [x] Accumulate non-empty `delta.ReasoningContent` into `reasoningBuf` (do not emit per-delta)
- [x] At `io.EOF`, before appending `EventMessageStop`: if `!emittedText` and `reasoningBuf` is non-empty, append an `EventTextDelta` carrying the buffered reasoning text
  - [x] Confirm ordering: any open tool-call stop is flushed, then the recovered text delta, then `EventMessageStop` — verify against the existing EOF flush block
- [x] Add tests in `stream_test.go` for the four streaming cases above
- [x] Run `go test ./internal/llm/openai/...` from `source/server`

## Phase 3 — End-to-end GLM verification

Objective: prove the adapter recovery restores visible GLM output before touching any defaults. This is the gate for Phase 4.
Files: none required beyond a throwaway probe script; no production edits in this phase.
Tests: live probes against a running GLM llama-server instance.

- [ ] Ensure a GLM-4.5-Air llama-server instance is running (via `/m` runtime dashboard `start`, not manual spawn; per prior decision, use `StartRuntimeModel`)
- [ ] Probe non-streaming single-turn: assert visible content is non-empty
- [ ] Probe non-streaming multi-turn with prior assistant history (the shape that previously emptied `content`): assert visible content is non-empty
- [ ] Probe streaming single-turn: assert visible text is streamed
- [ ] Probe streaming multi-turn: assert visible text is streamed
- [ ] Record probe results (char counts / sample output) in this effort dir as evidence for the Phase 4 gate

## Phase 4 — Catalog and configuration update (gated on Phase 3)

Objective: with recovery proven, stop leaving GLM marked/routed as broken. Update catalog status and the relevant config/default routing so GLM is usable through normal tiers. Do NOT start this phase until Phase 3 evidence is recorded.
Files: `source/server/internal/localruntime/llamaserver/catalog.json`, `source/server/internal/localruntime/llamaserver/catalog.go` (only if the loader guard needs to accept GLM), `source/server/internal/localruntime/llamaserver/catalog_test.go`, `~/.config/cercano/config.yaml` (live machine config), and the bootstrap/default template if it seeds recommended models.
Tests: catalog loader tests updated to reflect GLM as plain-chat-capable; catalog validity tests still pass; no other model’s tier assignment regressed.

- [ ] Flip GLM catalog entry: `plain_chat_ok: true` and clear/adjust `status: "broken"`
- [ ] Update `catalog_test.go` expectations that currently assert GLM is broken / plain-chat-not-ok (`TestCatalog_GLMFlaggedPlainChatBroken`, `TestLoadCatalog_RejectsNonPlainChatInChatTier` interactions)
- [ ] Repoint the intended local tiers to GLM per spec (everyday / fast_light / fast_light_text / most_capable for high-memory profiles) in catalog profiles `96` and `128`; keep embedding on `nomic-embed-text-v1.5-f16`; leave `24`/`48` on qwen
- [ ] Update live `~/.config/cercano/config.yaml`: set `models.open.overrides.llama_server` for the intended tiers to `glm-4.5-air-q4_k_m`; remove the conflicting `default_model` inconsistency noted in the spec
- [ ] Update the bootstrap/default template so first-run users on capable machines get the same routing
- [ ] Run `go test ./...` from `source/server` (catalog + loader + any config tests)

## Phase 5 — Cleanup and checkpoint

Objective: land the change cleanly.
Files: any probe scripts created in Phase 3.

- [ ] Remove throwaway probe scripts
- [ ] Checkpoint the adapter fix, tests, catalog/config updates with a clear conventional-commit message
- [ ] Confirm `go build ./...` and the targeted test suites pass from `source/server`
