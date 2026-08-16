# Plan: Streamed Conversation Resume

## Current code path snapshot

- Protobuf source: `source/proto/agent.proto` defines unary `ResumeConversation(ResumeConversationRequest) returns (ResumeConversationResponse)` plus `PersistedTurn`.
- Generated Go bindings: `source/server/pkg/proto/agent.pb.go` and `source/server/pkg/proto/agent_grpc.pb.go` are committed and regenerated from `source/proto/agent.proto`.
- Server handler: `source/server/internal/agent/agent.go` implements `ResumeConversation` by delegating to the persistence service.
- Persistence service: `source/server/internal/hostsvc/persistence/persistence.go` converts stored conversation turns to protobuf `PersistedTurn`s.
- Storage: `source/server/internal/conversation/store.go` currently has `GetTurns(ctx, conversationID) ([]Turn, error)`, which loads every row into memory.
- Client wrapper: `source/server/pkg/agentclient/client.go` exposes `ResumeConversation(ctx, conversationID) ([]*proto.PersistedTurn, error)` and currently calls the unary RPC.
- CLI call sites: `/resume`, history picker resume, main apply-resume, sub-agent restore, and sub-agent reopen all use the same client wrapper, so the CLI can remain mostly unchanged if the wrapper collects stream chunks internally.

## Phase 1 — Protobuf API and generated bindings

- [x] Update `source/proto/agent.proto` additively:
  - [x] Add `message ResumeConversationChunk { repeated PersistedTurn turns = 1; }`.
  - [x] Add `rpc StreamResumeConversation(ResumeConversationRequest) returns (stream ResumeConversationChunk);` beside the existing unary resume RPC.
  - [x] Keep existing field numbers and existing unary RPC unchanged.
- [x] Regenerate Go protobuf bindings into `source/server/pkg/proto` using the repository's existing protoc workflow, for example:
  - [~] `cd source/server && protoc --go_out=. --go-grpc_out=. --proto_path=../proto ../proto/agent.proto`
  - [x] Adjust the command if the generated path options require the existing package layout. The repository's single source of truth is `source/proto/generate.sh`; current edit includes a manual compatibility shim until regeneration can be run.
- [x] Confirm generated service interfaces include the new server-streaming client and server types.

## Phase 2 — Shared chunking logic

- [x] Introduce a small helper near the persistence/resume code to batch `conversation.Turn`s or protobuf `PersistedTurn`s into byte-budgeted chunks.
- [x] Use a conservative default chunk target, preferably 8 MiB or 16 MiB, well under the current 64 MiB gRPC message cap.
- [x] Estimate turn size from string field lengths (`id`, `conversation_id`, `role`, `content`, `content_json`) and a modest fixed overhead for numeric fields / protobuf tags.
- [x] Flush a chunk before adding a turn that would cross the budget.
- [x] Send an oversized single turn alone only if it is still below a hard safety ceiling; otherwise return a clear error explaining that an individual turn is too large to stream safely.
- [x] Keep ordering stable: `created_at ASC, rowid ASC` from storage through stream delivery.

## Phase 3 — Server streamed resume handler

- [x] Add `StreamResumeConversation` to the gRPC front door and persistence implementation (`source/server/internal/server/server.go` and `source/server/internal/hostsvc/persistence/persistence.go`).
- [x] Preserve existing resume side effects:
  - [x] The server should still reload or rehydrate the agent's active conversation/session state as unary resume does today.
  - [x] The transcript sent to the CLI should match unary resume's `PersistedTurn` conversion.
- [x] Implement streaming by sending bounded `ResumeConversationChunk` messages until all turns are delivered.
- [x] Return errors before or during the stream with normal gRPC status errors where practical.
- [x] Keep unary `ResumeConversation` working for compatibility; it may continue using the existing full-load response path.

## Phase 4 — Optional persistence iteration improvement

This can be done in the first implementation if it stays small; otherwise defer after the streaming RPC works.

- [-] Prefer adding an iterator/callback method, for example `ForEachTurn(ctx, conversationID, func(Turn) error) error`, to avoid creating one giant `[]Turn` on the server before streaming. Deferred: this change fixes the transport ceiling first while preserving existing rehydration behavior.
- [-] If adding the iterator:
  - [-] Reuse the same SQL ordering as `GetTurns`.
  - [-] Keep the store mutex and row lifetime simple and correct.
  - [-] Use the iterator only in the streamed handler; leave `GetTurns` for existing callers.
- [x] If deferring the iterator:
  - [x] Note in comments or follow-up that streaming fixes transport size first but does not yet eliminate server-side full transcript allocation.

## Phase 5 — Agent client wrapper

- [x] Change `source/server/pkg/agentclient/client.go` so `Client.ResumeConversation(ctx, conversationID)` calls `StreamResumeConversation`.
- [x] Collect chunks into a single ordered `[]*proto.PersistedTurn` and return that to preserve the exported wrapper API.
- [x] Treat an empty stream as an empty transcript, not an error.
- [x] Propagate mid-stream receive errors with enough context to diagnose resume failures.
- [x] Leave any direct generated unary client access untouched for compatibility.

## Phase 6 — CLI timeout and validation call cleanup

- [x] Review CLI resume contexts that currently use short fixed timeouts:
  - [x] Main `applyResume` uses around 5 seconds.
  - [x] Sub-agent restore/reopen use around 5 seconds.
  - [x] Slash `/resume <id>` validates by calling full resume with around 3 seconds.
- [x] Replace the slash validation full-resume call with a cheaper existence check if one already exists, or skip eager validation and let `applyResume` surface the streamed resume error.
- [x] Increase or remove inappropriate short deadlines around full transcript resume so a large streamed transcript is not cancelled unnecessarily.
- [x] Keep user-facing error messages plain and actionable.

## Phase 7 — Tests

- [x] Add a focused server/client integration test that starts a test gRPC server with the existing 64 MiB limits and verifies `agentclient.Client.ResumeConversation` can resume a synthetic transcript whose total persisted payload is larger than 64 MiB.
- [x] Keep individual chunks far below 64 MiB in that test to prove streaming, not a raised cap, is the fix.
- [x] Add a smaller unit test for chunk boundary behavior:
  - [x] preserves ordering;
  - [x] creates multiple chunks when the byte target is crossed;
  - [x] handles empty transcripts;
  - [x] handles a single large turn according to the chosen guard behavior.
- [~] Add or update compatibility coverage proving unary `ResumeConversation` still returns the full transcript for a small conversation. The client now falls back to unary when the stream RPC is unimplemented, but this still needs test execution/coverage review.
- [x] Run package-level tests around changed server packages and agent client.

## Phase 8 — Build and verification

- [x] Run formatting on touched Go files: `gofmt`.
- [x] From `source/server`, run targeted tests first, for example:
  - [ ] `go test ./internal/agent ./internal/hostsvc/persistence ./pkg/agentclient`
- [x] If protobuf generation or shared interfaces affect wider server code, run:
  - [ ] `go test ./...`
- [x] From `source/clients/cli`, run CLI tests if CLI files changed:
  - [ ] `go test ./...`
- [x] Build both modules per self-development guidance:
  - [ ] `cd source/server && go build ./cmd/cercano`
  - [ ] `cd source/clients/cli && go build ./cmd/cercano`
- [ ] If practical, manually test resuming `LUNIE` after restarting the dev agent so the singleton is not serving stale code.

## Phase 9 — Documentation / follow-up notes

- [x] Add a brief comment near the streaming RPC or client wrapper explaining why resume uses streaming despite returning a collected slice to callers.
- [ ] If server-side iteration is deferred, record a follow-up note in the final response: transport is fixed, server still temporarily holds the full transcript in memory.
- [ ] If future lazy rendering is desirable, record pagination/lazy transcript browsing as a separate product design rather than mixing it into this transport fix.

## Risks and mitigations

- **Generated protobuf drift:** regenerate from the single `source/proto/agent.proto` source and verify both generated Go files compile.
- **Mid-stream failure after partial receipt:** the wrapper should discard partial results and return the receive error; callers already surface resume errors.
- **Timeout-induced false failures:** remove `/resume` eager full-transcript validation and relax full resume deadlines where necessary.
- **Memory pressure remains if storage still full-loads turns:** prefer the iterator in Phase 4 if implementation is straightforward; otherwise explicitly separate that from the transport-size fix.
- **Oversized individual turns:** chunking cannot split one protobuf turn without changing semantics. Detect and return a clear error if a single turn would exceed the safe per-message ceiling.
