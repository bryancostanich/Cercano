# Plan: Clean push confirmation UX

Effort: `efforts/fix-push-prompt-ux`
Spec: `efforts/fix-push-prompt-ux/spec.md`

## Phase 1 — Clarify git delegation policy

- [x] Update the `delegate-git-plumbing` protocol in `source/server/internal/protocols/catalog.go` so it distinguishes noisy git plumbing from explicit user-authorized publishing.
- [x] Remove `git_push` from examples that suggest generic git work should be delegated.
- [x] Add wording: when the user explicitly asks to push/publish the current branch, use the scoped `git_push` capability directly rather than dispatching solely to avoid git output in the main context.
- [x] Update the `dispatch` capability description in `source/server/internal/capabilities/builtins/dispatch_cap.go` so it still recommends scoped tools over Bash inside delegated workflows, but does not steer simple explicit pushes through dispatch.

## Phase 2 — Turn delegated write/execute false success into an error

- [x] Change `source/server/internal/hostsvc/tools/tools.go` after `detectSuspiciousNoOp` so suspicious write/execute no-op dispatches return an error instead of a successful `dispatch.Result` with only advisory fields.
- [x] Preserve logging and progress events, including the sub-conversation id and suspicion reason.
- [x] Keep `dispatch.Result.Suspicious` fields available for any callers/tests that inspect structured results before error handling, but do not let the parent agent treat the run as successful.
- [x] Ensure read-only low-signal dispatches remain advisory/log-only and are not hard failures.

## Phase 3 — Tests

- [x] Add or update tests around `detectSuspiciousNoOp` / dispatch execution to assert that granted write/execute tools with zero write/execute calls produce an error.
- [x] Add a regression test for a read-only low-signal dispatch that still succeeds.
- [x] Add a protocol/description test if one exists for protocol catalog content; otherwise rely on focused package tests plus review of the string updates.
- [x] Run focused tests for touched packages:
  - [x] `go test ./internal/hostsvc/tools`
  - [x] `go test ./internal/capabilities/builtins`
  - [x] any protocol package tests if applicable.
- [x] Run broader server tests if shared interfaces or generated protocol catalogs are affected:
  - [x] `go test ./...`

## Safety notes

- Do not add approval caching or automatic reuse. The clean fix is one direct approval for a direct push, and hard failure for delegated false-success.
- Do not weaken `git_push`; it remains execute-tier.
- Do not block legitimate delegated workflows that actually call their granted write/execute tools.
