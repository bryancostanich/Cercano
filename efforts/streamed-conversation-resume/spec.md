# Streamed Conversation Resume

## Problem / motivation

Large persisted conversations can exceed the gRPC unary message limit when the CLI resumes them. The concrete failure was resuming the `LUNIE` conversation, whose persisted transcript is about 154 MiB, while the agent client and server currently assume 64 MiB gRPC messages. The current `ResumeConversation` RPC returns every persisted turn in a single unary response, so the transport rejects the response before the CLI can render or recover.

Raising the limit would make this one conversation work until another transcript grows past the new ceiling. The underlying problem is that resume treats a large ordered transcript as one transport message instead of a sequence of bounded messages.

## Goals

- Add an additive server-streaming resume RPC that sends a conversation transcript in bounded chunks.
- Switch the exported Go agent client `ResumeConversation(ctx, id)` wrapper to use the streaming RPC while preserving its current return type, so current CLI call sites do not need to know about chunking.
- Keep the existing unary `ResumeConversation` RPC for compatibility with older or external clients.
- Preserve resume semantics: resuming still rehydrates the agent-side in-memory session and context meter before the next chat turn, and the CLI still receives the full persisted transcript in order.
- Ensure individual streamed messages stay comfortably below the existing 64 MiB gRPC cap.
- Add tests that prove a transcript larger than 64 MiB can be resumed through the client wrapper without increasing the gRPC limit.

## Non-goals

- Do not implement lazy or partial transcript rendering in the CLI in this effort.
- Do not redesign conversation persistence or compaction.
- Do not remove or break the existing unary `ResumeConversation` RPC.
- Do not raise the gRPC max message size as the primary fix.

## Constraints

- The protobuf change must be additive so generated bindings remain backward-compatible for existing clients.
- The CLI and server remain separate modules: the CLI should continue consuming the server module only through exported packages such as `pkg/proto` and `pkg/agentclient`.
- Stream chunks must preserve persisted turn ordering.
- Server-side chunking must account for large individual turns. Normal chunks should be sized by estimated payload bytes, with a guard for single-turn payloads that are themselves unusually large.
- Existing timeout behavior should be revisited where resume currently uses short fixed timeouts, because streaming a very large transcript may legitimately take longer than a small unary call.
- Sub-agent tab restore uses the same resume wrapper and must continue to work.

## Decisions

### Transport shape: server-streamed batched resume

Chosen option: add a server-streaming RPC, for example `StreamResumeConversation(ResumeConversationRequest) returns (stream ResumeConversationChunk)`, where each chunk carries a bounded list of `PersistedTurn`s.

| Axis | Server-streamed batched resume | Unary paginated resume |
|---|---|---|
| Semantic fit | One logical resume operation delivered as multiple bounded transport messages. | Models resume as page navigation even though the CLI wants the whole transcript rehydrated. |
| Cost / complexity | Medium: protobuf/generated code, server handler, persistence service, client wrapper, tests. CLI call sites can mostly stay unchanged. | Medium: similar files plus page token or offset semantics and client loop state. |
| Risk | Main risk is mid-stream failure after partial receipt; this is loud and testable. | Main risk is consistency if turns are appended while paging; offset paging can skip or duplicate without snapshot/cursor care. |
| Reward | Removes the single-message size failure while preserving current resume semantics. Later can support progress display. | Also removes the size failure and would help future lazy history browsing. |
| Side effects | Keeps transport chunking hidden behind the Go client wrapper for now. | Risks leaking pagination concepts into resume callers unless carefully hidden. |
| Main drawback | Requires streaming RPC plumbing and chunk-boundary tests. | More stateful API for a flow that is not inherently user-paginated. |

The strongest case for pagination is that it would be a reusable foundation for lazy transcript browsing. That is a real future need, but it is not the immediate failure: current resume needs to restore the full transcript and sub-agent tabs. Streaming is the cleaner fit for a large ordered payload that is still one logical result.

### Compatibility stance: additive streaming RPC, unary retained

The existing unary `ResumeConversation` stays in the service. New code will use the streaming RPC through `agentclient.Client.ResumeConversation`, but retaining the unary RPC avoids a flag day for other clients and keeps older generated clients source-compatible.

### Client abstraction: preserve current wrapper API

The exported `agentclient.Client.ResumeConversation(ctx, conversationID)` should continue returning `[]PersistedTurn`. It will collect streamed chunks internally. This keeps the initial implementation focused on the transport bug and avoids a wider CLI refactor. A future UI improvement can expose progress or incremental rendering through a separate callback/iterator-style API if needed.

### Chunk sizing: byte-budgeted batches under the current message cap

Chunks should be batched by an explicit byte budget rather than a fixed turn count. The budget should leave generous headroom under the existing 64 MiB gRPC limit, for example a target around 8 MiB to 16 MiB per chunk. The implementation should estimate each turn from its string field lengths plus modest protobuf overhead, flush before adding a turn that would cross the budget, and send an oversized single turn alone with a clear error path if it cannot fit safely.

## Acceptance criteria

- Resuming the `LUNIE` conversation no longer fails with `ResourceExhausted` at the 64 MiB gRPC receive limit.
- Unit or integration coverage demonstrates a resume transcript whose total payload exceeds 64 MiB is delivered successfully as multiple streamed chunks while the gRPC cap remains 64 MiB.
- Existing small resume behavior still works through the exported `agentclient.Client.ResumeConversation` API.
- The old unary RPC still compiles and returns the full transcript for compatibility.
- CLI `/resume`, history-picker resume, and sub-agent tab restore continue to use the same high-level resume wrapper and render ordered turns correctly.
