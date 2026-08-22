# Image History Context Guard Plan

## Objective

Prevent image-heavy conversation history from overflowing provider context windows by making historical images references instead of prompt payloads, making context budgeting image-aware, and adding provider-side invariant checks.

## Phase 1 — Confirm current seams and tests

- [x] Re-read the narrow implementation seams before editing:
  - `source/server/internal/agent/vision_placeholder.go`
                                        - `source/server/internal/agent/toolloop.go`
                                        - `source/server/internal/compaction/tokens.go`
                                        - `source/server/internal/llm/responses/adapter.go`
                                        - existing tests under `source/server/internal/agent`, `source/server/internal/compaction`, and `source/server/internal/llm/responses`
- [x] Verify whether the provider request path already always calls `RewriteImagesToPlaceholders` after send-view construction, and identify any direct adapter tests/helpers that need an explicit raw-image allow flag.

## Phase 2 — Add conservative image-aware budgeting

- [x] Update the compaction/context budget estimator so `llm.BlockImage` contributes cost when it contains inline image data or an image URL.
- [x] Use a conservative approximation based on encoded byte length, not zero. The exact formula can be simple and documented, for example one token per four base64 characters plus a small fixed overhead, or a byte-length-derived upper bound consistent with the existing estimator style.
- [x] Add tests in `source/server/internal/compaction/tokens_test.go` proving:
  - image-only messages have non-zero token cost;
                                - a large inline base64 image costs substantially more than a small text message;
                                - segmentation can split an image-heavy history under a tight budget.

## Phase 3 — Make historical image sanitization explicit before provider calls

- [x] Ensure the provider-facing history path applies `RewriteImagesToPlaceholders` after compaction/send-view assembly and before any `llm.Chat` call.
- [x] Preserve current behavior where registered images remain inspectable via `inspect_image` through `VisionStore`.
- [x] If there are paths where `VisionStore` may be nil or conversation ID blank, decide locally whether to fail closed for provider calls with raw images or preserve legacy behavior only where a caller explicitly opts into raw images.
- [x] Add or extend tests in `source/server/internal/agent` proving provider-facing history does not contain `BlockImage` after the rewrite when a vision store and conversation ID are available.

## Phase 4 — Add OpenAI Responses serialization guard

- [x] Introduce an explicit guard in the Responses adapter so raw inline images are rejected unless a deliberate allow-raw-images path is used.
- [x] Keep existing image serialization support available for intentionally raw image tests or future vision-provider use, but make default provider calls fail locally rather than silently emitting giant `data:image/...;base64,...` payloads.
- [x] Update `source/server/internal/llm/responses/adapter_test.go`:
  - existing raw image serialization test should either opt into raw image allowance or be split into an explicit allow-raw test;
                - add a default-path test asserting inline image blocks are rejected or omitted according to the chosen adapter API;
                - keep URL passthrough behavior only if it is explicitly allowed by the same policy.

## Phase 5 — Regression for the observed failure mode

- [x] Add a focused regression test that builds a history resembling the failing conversation tail: normal text plus one or more very large `BlockImage` blocks.
- [x] Assert the provider-facing representation contains placeholders/inspect IDs and no base64 data URL.
- [x] Assert budget accounting sees that history as large enough to affect compaction decisions.
- [x] Keep this at unit-test level unless the existing runner seams make a small integration test cheaper and clearer.

## Phase 6 — Validate

- [x] Run the narrow relevant Go tests first:

```bash
cd source/server
go test ./internal/compaction ./internal/agent ./internal/llm/responses
```

- [x] If provider interfaces or runner seams changed, also run the smallest broader server package set that covers compilation of provider invocation:

```bash
cd source/server
go test ./internal/runner ./internal/llm/... ./internal/agent
```

If the changes touch public interfaces used elsewhere, finish with:

```bash
cd source/server
go test ./...
```

## Completion criteria

- Historical image blocks cannot reach normal OpenAI Responses serialization as inline data URLs by default.
- Image payloads contribute to context/compaction budgeting.
- Existing current-turn/vision-tool behavior remains test-covered.
- The observed class of failure is covered by a regression test.
- Relevant tests pass.
