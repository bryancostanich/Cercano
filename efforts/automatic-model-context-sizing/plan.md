# Automatic model context sizing

Implement the approved spec.md. The user approved optional integer configuration, runtime-owned capacity, and the specification. This plan is pending execution approval. Scope is managed llama-server; preserve explicit settings and other runtimes. Do not alter live user config, restart live runtimes, or push changes without separate authorization.

## Phase 1 — Establish regressions and interface map

Objective: reproduce defects before fixes and identify every consumer needed for runtime-owned context. Files to inspect: source/server/pkg/config, cmd/cercano setup, internal/localruntime/llamaserver, internal/localruntime types and manager, internal/gguf, internal/modelwindow, internal/contextmeter, engine/llamaserver, host services, runner, worker, dispatch, proto, agentclient, and CLI setup/context surfaces. Tests to write: absent-key round trip and setup persistence regressions plus a stale-config versus running-window regression at the relevant service seam.

- [x] Create an isolated implementation worktree using git_worktree; delegate repository inspection and preserve unrelated changes
- [x] Read repository agent, CLI, and self-development instructions in the execution worktree
- [x] Reproduce Load → Save → Load with an absent context_size using focused tests and observe failure
- [x] Map setup saves, size assignments, config snapshot copying, launch flags, startup readiness, runtime properties, instance adoption, and budgeting consumers
- [x] Inspect installed/runtime response contracts without launching or restarting a live model; distinguish per-slot capacity from total allocation
- [x] Add and observe a regression demonstrating that config-based reconstruction can disagree with a serving instance
- [x] Confirm metadata and memory-estimation prerequisites; pause for approval if findings invalidate the spec

## Phase 2 — Represent automatic config explicitly

Objective: replace the implicit default and presence flag with an optional override without changing other runtime defaults. Files: source/server/pkg/config/config.go and tests; setup in cmd/cercano/main.go and tests; all identified llama-server config assignments, copies, and config editor consumers. Tests: absent and null omission behavior consistent with the optional-field contract, repeated save/reload, explicit 8192 and 65536, invalid nonpositive values, unrelated settings saves, snapshot isolation, and setup persistence.

- [x] Change llama-server ContextSize to *int with omission on serialization and no default allocation
- [x] Remove ContextSizeSet and its now-redundant YAML presence parser
- [x] Validate explicit zero and negative sizes at configuration mutation/load boundaries
- [x] Update intentional setters and preserve snapshot isolation when copying pointer-bearing config
- [x] Remove setup's generic 8192 assignment; preserve automatic mode through both setup paths
- [x] Update consumers and fixtures without introducing replacement fallback constants
- [x] Run targeted config/setup tests and checkpoint the coherent change with explicit paths

## Phase 3 — Resolve a safe planned context

Objective: make one runtime-owned resolution produce the concrete setting used by memory validation and launch. Files: internal/localruntime/llamaserver/context_size.go, provider.go, related tests and metadata helpers; internal/gguf only if required by verified gaps. Tests: precedence, native metadata, missing/invalid metadata, deliberate 8K profiles, memory refusal, and conflicting flags.

- [x] Resolve explicit override → applicable RAM profile → model launch setting → GGUF ContextLength
- [x] Return the resolved size and provenance or an actionable error; remove universal numeric fallback
- [x] Normalize context launch arguments so duplicate/global/model flags cannot silently override the resolved setting
- [x] Feed the same concrete size to memory estimation and explicit launch arguments
- [x] Ensure missing memory-sizing evidence is not treated as proof of safe automatic allocation
- [x] Preserve explicit overrides' applicable memory checks and avoid automatic shrinking
- [x] Run focused resolver and memory-guard tests; checkpoint explicit paths

## Phase 4 — Publish and confirm runtime capacity

Objective: expose instance-bound planned and confirmed context through runtime services. Files: internal/localruntime/types.go, manager lifecycle, llama-server provider startup/health handling, and runtime service/protocol/client interfaces identified in Phase 1. Tests: preparation, readiness/property parsing, per-request capacity, missing confirmation, restart, startup failure, stop, replacement, adoption, and stale-config behavior.

- [x] Add runtime-owned capacity state with model/instance identity, size, provenance, and planned/confirmed distinction
- [x] Publish planned capacity without presenting it as observed serving capacity
- [x] Confirm actual per-request capacity from the running server before authorizing inference against it
- [x] Handle missing or contradictory confirmation without substituting a guessed window
- [x] Invalidate confirmed capacity across stop, restart, failed startup, and instance replacement
- [x] Confirm adopted instances rather than infer their capacity from current config
- [x] Extend existing service/client/protocol interfaces as required; regenerate bindings using repository tooling
- [x] Run runtime and changed-interface integration tests; checkpoint explicit paths

## Phase 5 — Consume runtime-owned capacity everywhere

Objective: make local budgeting and context display reflect the serving process rather than reconstructing its window. Files: internal/modelwindow, contextmeter, engine/llamaserver, modelbudget, requestassembly, dispatch, runner, worker, host services and CLI context/setup consumers as identified in Phase 1. Tests: confirmed capacity propagation through direct, delegated, worker, and research paths; unknown-window handling; active-config edits; non-llama-server regressions.

- [x] Replace independent llama-server config/catalog window reconstruction with runtime-owned state
- [x] Ensure startup/preparation establishes the necessary capacity before request authorization
- [x] Prevent unknown local capacity from being promoted to a known family/model maximum
- [x] Propagate confirmed capacity through dispatch targets, worker boundaries, and request accounting
- [x] Show effective capacity and source accurately; distinguish planned from confirmed in setup/context surfaces where applicable
- [x] Verify configuration edits do not change the advertised capacity of an existing process
- [x] Run consumer unit tests and interface integration tests; checkpoint explicit paths

## Phase 6 — End-to-end contract verification and documentation

Objective: verify the complete setup/config/runtime/budgeting contract without unnecessary full-system tests or live-machine mutations. Files: focused integration tests across affected packages, CLI tests, and existing setup/config/runtime documentation. Tests: setup save/reload through mocked runtime launch and capacity confirmation to budgeting, automatic model metadata path, safety refusal, and unchanged other runtimes.

- [x] Verify repeated setup and unrelated settings saves preserve automatic selection
- [x] Verify explicit 8192 and 65536 survive and are honored subject to validation
- [x] Exercise the integrated automatic resolution → memory check → launch → confirmation → budget path using controlled fixtures
- [x] Verify slot-aware capacity, unknown metadata, failed confirmation, and lifecycle invalidation cases
- [x] Run targeted server/CLI package tests, interface integration tests, affected builds, and applicable race tests for shared runtime/config state
- [x] Audit managed llama-server paths for remaining generic 8K substitutions without removing intentional model-specific 8K values
- [x] Document automatic selection, precedence, actionable errors, and the manual cleanup needed for historical accidental overrides
- [x] Obtain adversarial review of the approved contract and resolve findings with regression tests
- [x] Record verification results and limitations; checkpoint only effort changes, with no push

## Execution constraints

Use the executing-plans protocol after approval. Delegate mechanical multi-file edits and git operations to available sub-agents; if required tools are unavailable, report the blocker instead of claiming execution. Every bug fix must follow an observed failing regression/probe. Match test scope to affected interfaces, and do not claim live integration verification from mocked tests. Spec changes or foundational surprises require renewed human approval. Request autonomous execution separately after plan approval.
