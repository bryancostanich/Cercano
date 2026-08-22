# Image History Context Guard

## Problem / motivation

A long-running MCP-heavy conversation can accumulate large image blocks in the persisted live tail. In the observed `LUNIE - GLOBE PORT V - MCP UNIFICATION` conversation, several screenshot turns after the compaction boundary contributed roughly 24.5 MB of base64 PNG data. A later OpenAI Responses turn replayed enough of that tail to exceed the provider's context window, even though the final user/test turns were small.

This violates Cercano's intended model: historical images should be referenced and inspected through the `inspect_image` tool, not repeatedly sent as raw prompt payloads. It also exposes an accounting gap: compaction token estimation currently counts text/tool content but does not account for image payload size, so image-heavy tails can appear cheap to the budget logic.

## Goals

- Prevent historical image payloads from being serialized into normal provider requests.
- Preserve the existing vision-as-tool workflow: images remain addressable through stable `inspect_image` placeholders when the in-memory vision store can hold them.
- Make compaction and context budgeting conservatively aware of inline image payload size.
- Add adapter-level protection so pathological oversized inline image payloads fail locally instead of becoming giant provider requests, while normal current-turn image serialization remains available for vision-capable paths.
- Cover the observed failure mode with focused regression tests.

## Non-goals

- Do not mutate or purge existing conversation database rows as part of the core fix.
- Do not redesign persisted attachment storage; the current `VisionStore` remains in-memory for this effort.
- Do not add thumbnailing, lossy image compression, or a new vision provider flow.
- Do not remove current-turn image support where it is explicitly intended.

## Constraints

- Historical conversation images are references, not prompt payloads.
- Current-turn images must not silently disappear; they should either be converted to inspectable placeholders or sent raw only through a deliberately allowed vision-capable path.
- The OpenAI Responses adapter must not be the first line of defense against oversized history, but it should enforce the invariant before serializing data URLs.
- Budget estimates may be approximate, but must be conservative enough that megabytes of base64 image data cannot look like a tiny text tail.
- Tests should stay at the narrowest useful tier: unit tests for token budgeting, placeholder rewriting, and Responses serialization guards; integration tests only if unit seams cannot prove the invariant.

## Decisions

### Chosen approach: combine placeholder rewrite with image-aware accounting

The approved product invariant is: history images are references, not prompt payloads; budgeting must account for their storage size; provider adapters should enforce that invariant.

| Decision axis | Placeholder rewrite only | Image-aware accounting only | Strip/thumbnail old images | Combined placeholder rewrite + image-aware accounting |
|---|---|---|---|---|
| Correctness | Prevents many provider overflows, but leaves budgeting blind. | Triggers compaction earlier, but still relies on compaction/provider ordering. | Reduces payloads, but changes data retention semantics. | Prevents raw historical image replay and makes the budget notice image-heavy tails. |
| User-facing behavior | Keeps images available through `inspect_image` when stored. | No behavior change, but not sufficient by itself. | Risks surprising users by deleting or degrading prior visual context. | Matches the intended vision-as-tool workflow without database mutation. |
| Risk | Could hide a fresh attachment if applied at the wrong boundary. | Provider calls can still fail before compaction catches up. | Highest semantic risk. | Slightly more code, but each guard is simple and testable. |
| Implementation cleanliness | Good partial fix. | Good backstop, not the core invariant. | Feels like cleanup policy rather than architecture. | Cleanest complete fix for the observed failure mode. |

The implementation should therefore do both: explicitly sanitize historical image blocks before provider calls, and teach compaction/budget code to charge image payloads conservatively. Provider adapters, especially OpenAI Responses, should additionally fail locally if an oversized inline image payload reaches serialization.

## Current code facts anchoring the work

- `source/server/internal/compaction/summarizer.go` renders frozen image blocks as `[image]`, so compacted/frozen history does not send image pixels to the summarizer.
- `source/server/internal/compactor/compactor.go` builds the send view from the summary plus live turns after `FrozenThrough`; live turns are currently reconstructed with raw blocks intact.
- `source/server/internal/agent/history.go` unmarshals persisted `BlocksJSON` directly into `llm.Message` blocks.
- `source/server/internal/agent/vision_placeholder.go` already provides `RewriteImagesToPlaceholders`, with tests in `vision_placeholder_test.go`.
- `source/server/internal/compaction/tokens.go` currently counts text/tool fields but not `BlockImage.ImageData` size.
- `source/server/internal/llm/responses/adapter.go` serializes `BlockImage` as `input_image` data URLs, so it needs an explicit oversized-inline-payload guard without disabling normal image-capable Responses calls.
