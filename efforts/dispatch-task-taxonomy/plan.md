# Task-class routing and destination redirects

Implement the user-approved spec in this directory. Work in Cercano on main as explicitly requested; do not create a branch or worktree. Preserve unrelated changes. This plan is pending execution approval; no implementation or tests were performed during planning.

Extend the existing configuration, routing transport, destination selection, and settings draft mechanisms rather than creating parallel systems. Preserve task identity and quality through redirects, apply existing locality restrictions at execution, and do not introduce fallback edges. Light uses the existing Economy/fast_light model tier; this effort does not rename persisted quality values.

Read-only inspection confirmed: config currently accepts only chat/dispatch; unknown task lookup defaults to Primary/Premium; dispatch.Spec has RoutingTask but empty means legacy role policy; Target, PreparedTarget, and Dispatch share resolve; routingwire and agentclient carry string-keyed assignments; UpdateRoutingAssignments validates a complete draft before applying it; Cloud owns routing controls and dirty-state handling; host and worker each select Chat destinations and expose candidate snapshots. These are inspection findings, not runtime verification.

Delegate repository inspection and well-specified multi-file work, and checkpoint completed units with explicit paths and conventional-commit subjects and bodies. Do not claim test execution or commits without tool evidence. Report any actual tooling blocker rather than bypassing it. Never push without a separate request.

## Phase 1 — Establish baseline and contract tests

Objective: verify the starting state and capture the smallest failing routing contracts before changing production code. Files: existing tests under source/server/pkg/config, internal/dispatch, internal/inference, internal/worker, internal/capabilities/builtins; source/clients/cli/internal/ui tests. Tests: focused class/default, redirect, exclusion, and draft-ownership reproductions.

- [x] Delegate repository-state inspection, confirm main and record SHA, and inventory existing changes without modifying or committing unrelated work.
- [x] Re-read project instructions and approved spec; inspect build/protobuf generation commands and existing focused test fixtures.
- [x] Complete a scoped caller inventory across generic dispatch, review, research budget/execution, Git land, host/worker watchdog, co-processor, local offload, schemas, aliases, and agent guidance. Record which calls carry classes and which are explicit exclusions.
- [x] Write and run minimal failing tests for seven approved defaults, unknown explicit class rejection, ordinary missing-class Default dispatch, redirect resolution/cycles, and excluded caller preservation. Capture actual failures before fixing them.
- [x] Establish focused package baselines and distinguish pre-existing failures from effort failures. Do not repair unrelated failures silently.
- [x] Record baseline evidence and checkpoint only this effort's completed test/documentation unit when appropriate.

## Phase 2 — Shared configuration and transport

Execution note (2026-09-12): the user approved explicit `secondary_redirect` and `local_redirect` fields, then authorized their implementation. Complete this bounded redirect configuration/transport unit first; the taxonomy tasks below remain pending. Omitted scalar values mean own configuration, and present complete transport drafts can clear them.

Objective: make taxonomy, defaults, and redirects consistent across saved configuration, settings calls, and worker snapshots. Files: source/server/pkg/config/routing.go, config.go and tests; source/proto/agent.proto; generated source/server/pkg/proto bindings; internal/routingwire/routing.go; pkg/agentclient/routing.go; internal/server/routing_settings.go; internal/hostsvc/config; internal/worker/wire.go and related tests. Tests: validation, sparse persistence, cloning, reset/absence, invalid-update atomicity, client/server and worker round trips.

- [x] Add shared task keys, validation, labels/default metadata, and approved assignments: reconnaissance Local/Light; mechanical_development Local/Standard; investigation, implementation, review, research Secondary/Premium; git_land Local/Premium.
- [ ] Preserve saved Chat/dispatch assignments and unset behavior. Reject unknown explicit task classes at configuration and invocation boundaries rather than falling through to Primary or Default dispatch.
- [x] Add persisted Secondary and Local redirect settings with omission meaning own configuration. Support only the approved targets; leave Primary without a redirect control.
- [x] Implement shared final-destination resolution that preserves the original task assignment and quality, follows chains, and validates cycles before mutation. Keep configured profile bindings separate from effective routing so disabling a redirect restores the saved setup.
- [x] Extend existing RoutingAssignments transport and client cloning/conversion for redirects; use the established absent-message versus present-complete-draft semantics. Regenerate bindings with the repository's generation workflow.
- [ ] Carry redirects and all class assignments in worker snapshots, including all profiles needed by effective cloud routes and their backups; keep credentials outside serialized configuration.
- [ ] Validate complete routing drafts atomically; reject invalid keys/targets/cycles without partial persistence. Verify clearing redirects and class overrides restores defaults.
- [ ] Run focused config, routingwire, agentclient, server settings, and worker snapshot tests; checkpoint explicit phase paths.

## Phase 3 — Unified effective routing in host and worker

Objective: resolve class and explicit quality, then redirects, then provider/model using a consistent configuration snapshot. Files: internal/dispatch/engine.go and startup_fallback.go; internal/inference/router.go and task_assignment.go; internal/hostsvc/providers/providers.go; internal/server/models_resolve.go; internal/worker/worker.go and worker_dispatch.go; internal/runner and internal/usage wrappers where necessary; cmd/cercano/main.go wiring. Tests: matching budget/execution routes, host/worker parity, provider refresh, model evidence, and fallback restrictions.

- [ ] Integrate shared redirect resolution into classified Chat and dispatch selection using the same candidate/config snapshot that provides task defaults. Do not independently read mutable settings midway through route/model resolution.
- [ ] Ensure explicit quality overrides work for every dispatch class without changing destination; retain existing explicit model-override semantics within locality bounds.
- [ ] Resolve model, cloud profile, credentials, backup chain, and model/context evidence from the final destination. Do not use the source destination's models or backups after a redirect.
- [ ] Make Target, PreparedTarget, Dispatch, host Chat, and worker Chat agree on effective route and quality. Preserve task identity through wrappers, agentic work, and worker transport without confusing free-form task prose with routing class.
- [ ] Distinguish ordinary missing-class dispatch from the explicitly excluded legacy callers. Default ordinary calls to TaskDispatch without globally rerouting watchdog, co-processor, or explicitly local offload behavior.
- [ ] Apply existing locality restrictions after redirect resolution. Preserve existing final-destination fallback policy, authentication handling, replay guards, and missing-model failure behavior; explicit redirects must not create automatic cross-destination fallback edges.
- [ ] Test all four direct redirect directions, Secondary-to-Local-to-Primary and Local-to-Secondary-to-Primary chains, disabled redirects, cycle rejection, unavailable final routes, locality prohibitions, backup model selection, and configuration changes between calls.
- [ ] Run focused dispatch, inference, host provider, runner, and worker tests, including relevant existing replay/authentication/context-evidence regressions; checkpoint explicit phase paths.

## Phase 4 — Classify producers and preserve exclusions

Objective: first-party model-assisted dispatch uses explicit class identity, while generic compatibility and excluded behavior remain intentional. Files: internal/capabilities/builtins/dispatch_cap.go, review.go, web_common.go, web_research.go, web_deep_research.go, gitflow_land.go, coproc.go, local_offload.go; internal/server/watchdog_wire.go; internal/worker/watchdog.go; host/worker dispatch wrappers and other producers identified in Phase 1. Tests: captured dispatch specs for schemas, class propagation, explicit quality, budget/execution agreement, and exclusions.

- [ ] Extend generic dispatch arguments and advertised schema with an optional explicit task-class selector using the approved keys. Omission uses Default dispatch; unknown explicit values fail validation. Update aliases/wrappers to preserve the field.
- [ ] Assign Review to standalone review paths and Research to both budget preparation and execution. Remove unintended fixed-quality overrides where they would defeat the approved class defaults; retain genuinely explicit per-invocation quality choices.
- [ ] Ensure first-party workflows select Reconnaissance, Mechanical development, Investigation, or Implementation explicitly at their semantic entry points, not by quality or prompt keyword inference.
- [ ] Carry Git land identity through the existing model-assisted landing workflow and nested inference gates without splitting it into independent Review tasks. Preserve deterministic Git mechanics, approvals, conflict handling, stopping conditions, tests, review gates, and continuation behavior.
- [ ] Mark or otherwise explicitly preserve the co-processor, watchdog, and local-offload exclusions at their producers and shared wrappers; test that redirects and changes to Default dispatch do not move them.
- [ ] Re-audit every first-party dispatch.Spec producer and wrapper against the inventory. Add tests with Default dispatch deliberately configured elsewhere to detect dropped class identity.
- [ ] Run focused builtins, dispatch wrapper, and host/worker watchdog tests; checkpoint explicit phase paths.

## Phase 5 — Dedicated Routing page

Objective: move routing ownership out of Cloud and expose class assignments and redirects with independent Save/Discard behavior. Files: source/clients/cli/internal/ui/config_tabs.go, settings_page.go, cloud_routing.go, cloud_commit.go, cloud_section.go, related navigation/controller files, and new routing-specific UI files as needed. Tests: tab rendering/navigation, draft persistence, save failures, discard, inheritance labels, redirect controls, and Cloud isolation.

- [ ] Add a Routing tab/page with destination setup and task-assignment sections. Move Primary/Secondary bindings, optional backups, and Chat/Default dispatch controls from Cloud instead of duplicating them.
- [ ] Add Secondary and Local redirect selectors, including own-configuration reset, and display the effective destination clearly without overwriting the source's saved profile choices.
- [ ] Add all seven task-class assignment rows with independent destination/quality controls and accurate inherited defaults; map Light to the existing economy value rather than changing persisted model taxonomy.
- [ ] Present the existing Local routing setup without duplicating runtime/model ownership or inventing a cloud-profile binding for Local. Keep model management in Local Models and profile/credential/model choices in Cloud.
- [ ] Separate routing draft/dirty/pending-navigation ownership from Cloud drafts. Preserve unsaved edits on refresh, and support Save/Discard, failed-save retention, navigation prompts, reconnect behavior, and atomic invalid-cycle errors.
- [ ] Update tab counts, keyboard/digit navigation, mouse hit testing, config shortcuts, and related snapshots/tests affected by the added page.
- [ ] Verify Cloud no longer exposes routing controls and neither page's save/discard accidentally applies or clears the other's draft.
- [ ] Run focused CLI UI and affected wizard tests plus a CLI build; checkpoint explicit phase paths.

## Phase 6 — Cross-layer verification and documentation

Objective: demonstrate the approved contract from settings through inference and leave accurate user/agent guidance. Files: cross-layer tests under internal/worker and internal/server; docs/agent/self-dev.md, docs/agent/sub_agents/README.md, existing cloud-routing and feature guides, affected tool guidance/protocol sources located by audit; effort verification notes and plan status. Tests: fake-provider integration, focused package regression, server/CLI builds, and manual UI smoke checks when available.

- [ ] Extend fake-provider integration from settings save through persistence/client transport, worker snapshot, dispatch/Chat selection, and recorded provider request. Verify exact task, quality model, final profile/destination, credential identity, and backup behavior without live inference.
- [ ] Cover all class defaults, saved overrides, explicit quality, missing/unknown classes, direct/chained redirects, cycle rejection with no mutation, reset/inheritance, and host/worker parity. Include Independent Default dispatch changes and excluded-caller regressions.
- [ ] Verify redirected cloud failures use only the final destination's approved backup/fallback behavior and preserve replay-safety restrictions; verify model/context metadata follows the actual serving route.
- [ ] Update user documentation for Routing, task defaults, redirects versus failover, locality restrictions, and Cloud/Local Models ownership. Update agent schemas/examples/guidance so first-party delegations explicitly select classes and no longer imply quality selects placement.
- [ ] Run affected server package integration tests and server/CLI builds. Expand test scope only when shared interface impact warrants it; document exact commands, results, and baseline blockers.
- [ ] Perform interactive Routing and Cloud smoke checks if the runtime is available; otherwise explicitly record that limitation. Do not claim live inference, manual interaction, or broad regression verification that was not performed.
- [ ] Delegate an explicit-path final review for spec compliance, dropped class identity, stale routing docs, excluded-caller changes, redirect cycles, and unrelated modifications. Resolve findings with focused reproductions and retests.
- [ ] Record verification outcomes and remaining limitations, update task status, and checkpoint only completed effort paths. Do not push.
