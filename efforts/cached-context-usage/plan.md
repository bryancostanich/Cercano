# Plan: cached context usage

Spec: `efforts/cached-context-usage/spec.md`

## Phase 1 — storage layer

- [x] pending — Add conversation_context_usage table to schema.sql
- [x] pending — Add ContextUsage struct and store get/save snapshot methods
- [x] pending — Update in-repo fakes implementing the conversation Store interface
  Verification: `go test ./internal/conversation`

## Phase 2 — write at compaction points

- [x] pending — Add nil-safe snapshot-persist seam to compactiongen.Generator
- [x] pending — Persist post-pass snapshot in runCompaction
- [x] pending — Persist snapshot in Regenerate and Clear
- [x] pending — Tests for compaction-path snapshot persistence
  Verification: `go test ./internal/compactiongen ./internal/compactor`

## Phase 3 — write at turn accounting

- [x] pending — Persist durable snapshot from recordRequestAccounting
- [x] pending — Test turn accounting persists a readable snapshot
  Verification: `go test ./internal/server`

## Phase 4 — proto and read path

- [x] pending — Add usage freshness fields to GetContextUsageResponse and regenerate
- [x] pending — Rework GetContextUsage to resolve live then snapshot then none
- [x] pending — Tests for snapshot path, missing snapshot, and live precedence
- [x] pending — Reconcile existing context-usage tests with the new contract
  Verification: `go test ./internal/server ./internal/hostsvc/...`

## Phase 5 — client and UI

- [ ] pending — Add freshness fields to agentclient.ContextUsage
- [ ] pending — Stop zeroing meter state on poll error in the CLI model
- [ ] pending — Surface stale/unknown state in the context view
- [ ] pending — Tests for retain-on-error and stale rendering
  Verification: `go test ./internal/ui/...` in `source/clients/cli`

## Phase 6 — end-to-end verification

- [ ] pending — Build server and CLI
- [ ] pending — Verify nonzero meter for conversation 230386d992670d7e
- [ ] pending — Verify meter survives agent restart before any turn
- [ ] pending — Full module test runs
  Verification: `go build ./...` both modules; `go test ./...` in
                                  `source/server` and `source/clients/cli`
