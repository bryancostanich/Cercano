# New-user setup reset

Implement the approved spec in `spec.md`. This plan is pending execution approval. No real settings, credentials, processes or downloaded files were changed during planning.

The command is terminal-only: `cercano reset --setup`. Reset is not a factory reset. Preserve history, conversations, downloads, installed runtimes and non-setup preferences; restore default model paths and seed a fresh wizard run. Implementation approval is not authorization to run the command against the user's real installation.

Use an offline, fail-closed reset path initially: require other clients and state-writing agent processes to be closed before modification. Do not add a live reset RPC or force an active agent to shut down. The specification permits but does not require automatic idle-agent shutdown; retaining a refusal path avoids building an unsafe approximation of quiescence. The command explains how to close clients and stop a manually started agent. Exclusive coordination must still prevent startup races after the check.

Planning evidence: `pkg/agentclient/launch_lock_unix.go` protects autolaunch only; it is not an agent-lifetime or configuration-writer lock. `internal/server/shutdown.go` closes the listening socket before draining has finished, so an unavailable port is not proof of stopped writers. The public `cercano` executable and standalone `cercano-cli` are separate entry points. New command dispatch must happen before normal agent startup, prerequisite checks or logging initialization that creates unrelated files.

## Phase 1 — Establish the baseline and reset inventory

Objective: confirm exact ownership and current repository state before implementing destructive behavior. Files: AGENTS.md, cmd/cercano/main.go, cmd/agent/main.go, pkg/config, pkg/agentclient, internal/secrets, auth packages, host configuration services, CLI main.go and internal/wizard. Add an effort verification/inventory document during execution. Tests: focused baseline suites only; no live credential or settings operations.

- [x] Read project instructions and the approved spec; delegate repository inspection and create an isolated worktree for this substantial effort using the available repository tools, preserving all unrelated work. Record branch and SHA. If required tools are unavailable, report the infrastructure blocker rather than inventing a branch or executing Git plumbing silently.
- [x] Inventory every Cercano-owned credential namespace/file, including orphaned API keys, OAuth access/refresh sets, legacy credential fields and any login-recovery persistence. Confirm which external authentication sources must remain untouched.
- [x] Enumerate exact setup fields and preserved fields in the current schema, plus wizard paths and runtime/model download metadata. Trace load/save migrations so legacy fields cannot resurrect a profile after reset.
- [x] Inventory all relevant state writers and launch paths: foreground/background agents, alternate agent executable, MCP mode, client reconnect/autolaunch, CLI wizard writes and direct configuration commands. Identify how each can participate in exclusive reset coordination.
- [~] Record focused baseline tests and add minimal failing reproductions for selective reset, orphan credentials, fresh setup with retained config, and startup/writer races. Observe the failing data before fixing behavior.

## Phase 2 — Selective configuration reset

Objective: build a pure, audited transformation that restores current installation defaults for setup settings while preserving the rest of the user's config. Files: pkg/config with a focused setup_reset.go and tests; a new internal/setupreset orchestration package may consume the transform later. Tests: table-driven clear/preserve cases, YAML fixtures, migrations and idempotency.

Initial clear boundary: OllamaURL; OpenRuntime/OpenModel/EmbeddingModel; legacy CloudProvider/CloudModel/CloudAPIKey/CloudBaseURL; CloudProfiles and all four destination profile bindings; TaskAssignments, SecondaryRedirect, LocalRedirect and LocusMode; LlamaServer and MistralRS runtime settings; Models and user ModelProfiles overrides; Compaction.SummarizerModel and Watchdog.Model. Validate this list against Phase 1, including any additional model-specific fields added since planning. Restore current Defaults semantics, not arbitrary zero values or old hard-coded model IDs.

Preserve the rest of Compaction and Watchdog, Agent settings, Port, ExecutionMode, WorkerIdleTimeoutSeconds, ToolLoop and unrelated/unknown keys unless Phase 1 proves a field is setup-owned under the approved spec. Do not widen deletion scope silently.

- [x] Implement a pure setup reset transformation with explicit field ownership and current default values. Deep-copy mutable data so the input and unrelated settings remain unchanged.
- [x] Implement safe YAML document editing that preserves unrelated and unknown fields rather than round-tripping the whole file through the typed Config serializer. Reject malformed or ambiguous input that cannot be transformed safely, including unsupported alias/merge cases affecting the reset boundary.
- [x] Validate transformed YAML with the ordinary config loader/migration behavior; prove that retired single-model, profile and credential fields do not reappear after load/save.
- [x] Test nondefault preserved settings, all reset categories, sparse/nil maps, missing config, repeated reset, unknown keys, malformed config and input immutability. Confirm no filesystem or keychain effects in the pure transform.

## Phase 3 — Shared fresh-wizard state

Objective: reuse the existing onboarding lifecycle when the config file remains present. Files: CLI internal/wizard/wizard.go and tests, CLI main.go/startup tests; extract the minimal shared persisted wizard state/path contract into a dependency location accessible to both executables, such as source/server/pkg/setupstate. Keep UI behavior in the CLI; the server command must not import a CLI internal package or duplicate its YAML schema.

Tests: shared path rules, persisted-state compatibility, fresh initial state, startup decision with retained config, baseline capture/rollback, completion and interrupted resume.

- [ ] Extract only the persisted state/path/load-save boundary needed by both executables, keeping existing wizard files readable and preserving CERCANO_WIZARD_STATE and XDG_CONFIG_HOME behavior.
- [ ] Provide a typed fresh-initial-state operation with no previous answers, selected profiles/models, completion markers or rollback baseline. Reuse the existing first step rather than introducing a competing setup-pending flag.
- [ ] Ensure the CLI recognizes fresh wizard state even when config exists, captures only the post-reset baseline, and cannot restore pre-reset settings when setup is cancelled or resumed.
- [ ] Test normal wizard completion and subsequent launch do not reopen setup unnecessarily; verify default-location downloads can be rediscovered without preserving old selections or custom directory overrides.

## Phase 4 — Exclusive offline coordination

Objective: make the reset mutually exclusive with relevant live writers and new launches, not merely with other reset commands. Files: shared process-state coordination package in source/server/pkg, pkg/agentclient launch/reconnect helpers and platform lock implementations, server command startup paths, CLI startup/wizard lifecycle and any additional writers identified in Phase 1. Tests: subprocess fixtures with private lock/config directories; no interaction with the developer's running agent.

Extend/reuse the existing launch-lock infrastructure where its semantics fit, but do not mistake its current short critical section for lifetime protection. Define one documented lock order for launch, writer lifetime and reset exclusivity. Acquire participation before reading mutable setup state. Do not deadlock autolaunch by holding a parent launch lock while a child needs that same exclusive lock to become ready.

- [ ] Implement the shared coordination primitive with bounded/cancellable acquisition, per-user/state-root ownership, restrictive file permissions and crash-safe operating-system lock release. Fail closed on unsupported platforms rather than using a fake lock.
- [ ] Have participating agents/clients/direct writers acquire the appropriate lease before reading/writing setup state and hold it for the required lifetime; let reset acquire exclusive access only when those leases are absent.
- [ ] Serialize or refuse competing startup while reset holds exclusive access. Use the same guard for direct agent launch and reconnect/autolaunch, not only one executable path.
- [ ] Refuse reset with an actionable message if an agent/client/writer is active, a legacy nonparticipating process is detected, or quiescence cannot be verified. Do not infer quiescence from port closure, issue a force-kill, or introduce a live destructive RPC.
- [ ] Test simultaneous reset, new launch, reconnect, wizard/settings writes, token refresh, draining agent, timeout/cancellation and reset-process crash. Verify no mutation on refusal and no old state can be repopulated after reported success. Escalate any unguarded writer or older-binary compatibility gap rather than claiming exclusivity.

## Phase 5 — Credential clearing and reset orchestration

Objective: implement an injected, testable operation with truthful partial-failure semantics. Files: internal/setupreset, internal/secrets adapters/tests, config and shared wizard helpers. Tests use fake credential stores and temporary files only.

Order: after confirmation, establish exclusive access; load and validate all inputs and destination paths; construct/validate reset config and fresh wizard state; establish credential enumeration/access; invalidate the old wizard baseline; delete the complete Cercano credential namespace; publish transformed config and fresh wizard state using restricted atomic writes. Never retain a backup of plaintext credentials or describe this multi-store operation as one transaction. Release guards on every exit.

- [ ] Define small injected interfaces for credential List/Delete, file publication, wizard reset and coordination; production code uses the real adapters while tests never touch the system keychain.
- [ ] Preflight config transformation, file ownership/path safety, writable destinations and credential enumeration before irreversible mutation. Handle missing state as a normal fresh installation while distinguishing absent credentials from an inaccessible store.
- [ ] Enumerate and delete all Cercano-owned credentials, including entries without a surviving profile. Do not fetch secret values for deletion, log tokens, revoke remote credentials or modify other applications' stores.
- [ ] Remove stale wizard answers/baseline and publish only the scoped config and fresh wizard state. Do not recurse over application directories or call runtime/model deletion operations. Ensure temporary files cannot expose legacy inline credentials or follow unexpected symlinks.
- [ ] Return phase-specific errors and an accurate partial-progress summary without secrets. Reruns must recover safely after each injected failure, including credential deletion, config publication and wizard publication failures.
- [ ] Test that file contents and metadata needed for download discovery, runtime binaries, conversation/history stores, preferences and custom-directory model files survive unchanged. Compare fixture hashes and protected-store contents before and after.

## Phase 6 — Terminal command and onboarding integration

Objective: expose the approved command without starting an agent, inference or downloads as an incidental side effect. Files: source/server/cmd/cercano command handling, focused reset command tests, CLI startup/wizard integration tests and help text. Reuse testable input/output interfaces instead of embedding destructive behavior in main.

- [ ] Recognize `reset --setup` before ordinary server/MCP startup, config fallback, runtime probing and side-effecting initialization. Strictly reject missing scope, unknown flags and extra arguments.
- [ ] Print the clear/preserve scope and require explicit interactive confirmation. Decline, EOF, help and malformed input must cause no filesystem/keychain mutation or process shutdown; noninteractive input must not silently approve. Do not add an unattended approval flag or interactive reset tool in this effort.
- [ ] Invoke the reset coordinator and map success, cancellation, refusal and partial failure to clear output and documented exit statuses. On success, explain the next interactive launch and optional custom-path reconfiguration; do not launch the UI automatically.
- [ ] Test the public command with injected stores and a private state root, including configured, empty, partially reset and malformed installations.
- [ ] Add a cross-layer fixture that resets populated setup state, preserves history/preferences/downloads, starts the normal CLI setup decision, confirms the first wizard step, verifies no old rollback baseline and demonstrates reuse of default-directory model files.

## Phase 7 — Verification, documentation and checkpoints

Objective: verify destructive boundaries and coordination at the appropriate test tiers, with no real-user reset. Files: affected server/CLI packages, command documentation, effort verification record. Checkpoint completed units with explicit paths; never push without a separate request.

- [ ] Run focused pure config/secrets/shared-state/orchestrator tests and complete affected command/client/wizard/UI packages; run coordination subprocess tests and race checks on the changed concurrency boundaries.
- [ ] Build the server and CLI entry points using their actual module layouts. Exercise command help, rejection and success only in a fully isolated fixture with a fake credential store; do not run the production reset adapter against the user's installation.
- [ ] Verify preservation of conversations/history, non-setup preferences, installed runtimes, model bytes and discovery metadata, and exclusion of external credentials. Audit every deletion target and error path against the approved spec.
- [ ] Independently review confirmation, namespace enumeration, symlink/path handling, lock order, startup/writer exclusion and failure recovery. Turn concrete findings into failing regressions before fixing them; report any unavailable review tools honestly.
- [ ] Document usage, required offline state, exact clear/preserve boundary, normalized/custom model-path behavior, external authentication limits and recovery after a partial failure. Clarify which executable opens the interactive setup in the current installation.
- [ ] Record exact commands/results and any platform/manual-testing limits, update semantic task statuses, and checkpoint only this effort's explicit paths after repository review. Preserve all concurrent work; no pushes and no real-installation reset.
