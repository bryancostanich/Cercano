# Baseline and execution blocker — 2026-09-12

The plan and autonomous execution were approved in conversation. Historical pending-approval wording in the spec is not the current execution approval state; the human-owned spec has not been rewritten.

## Repository baseline

Delegated audit reported main at d928619c, with only the untracked effort spec.md and plan.md present before this run. Preserve both. No production changes have been made in this run.

## Observed configuration reproduction

Added source/server/pkg/config/task_taxonomy_repro_test.go. Delegated execution ran, from source/server:

```sh
go test ./pkg/config -run TestTaskTaxonomy -count=1
```

Exit 1, expected: all seven approved new task keys resolve to Primary/Premium instead of their required assignments; all seven SetTaskAssignment calls fail with unknown task. Unknown-key rejection without mutation passes. The coordinator read the resulting test file to confirm its contents. This is an intentionally failing, uncommitted reproduction, not a completed implementation.

A separate delegated baseline command ran:

```sh
go test ./pkg/config ./internal/dispatch ./internal/routingwire -skip TestTaskTaxonomy -count=1
```

Exit 0: all three packages passed. No broader build, CLI test, integration, or live-inference claims are made.

A single-file delegated read confirmed routing.go currently declares only TaskChat and TaskDispatch; ResolveTask changes quality for TaskDispatch only, and validation/setters reject other keys. This explains the observed taxonomy failures.

## Partial caller audit

A delegated read-only shell search observed dispatch.Spec producers in agent/agent.go (coproc); server/watchdog_wire.go; builtins/local_offload.go; builtins/web_common.go (budget and execution); builtins/dispatch_cap.go; builtins/gitflow_land.go; builtins/review.go (two branches); builtins/coproc.go; and hostsvc/persistence/persistence.go. The result was truncated. This is NOT a complete caller inventory or wrapper audit. In particular, the persistence producer and worker watchdog still need inspection. Protobuf generation commands were not obtained.

## Tooling blocker

Single-step delegations completed, including the test creation/run, but broader baseline delegations twice failed validation because write-capable tools were granted and none were used despite reporting completion. Two read-only audits returned only their first supplied document rather than carrying out further reads. A narrowed two-Read diagnostic then failed with:

`openai-responses network: read tcp ...: read: connection reset by peer`

No production changes were attempted during the initial run. That run was paused, not complete; no commits or pushes were made at that point.

## Recovery baseline — 2026-09-12

Resumed step-by-step after the user requested recovery. Direct reads and test commands work. A single-call delegated Git audit succeeded, reporting main at d928619c and no changes outside this effort directory and the untracked taxonomy reproduction. A single-call delegated caller search also ran, but its output was truncated; it does not establish a complete inventory. Multi-step delegation remains unverified. Standing delegation requirements still apply; use bounded single-call delegations where required rather than assuming user permission changes those instructions.

Re-read AGENTS.md and the approved spec and plan. Inspected source/proto/generate.sh: invoke that script to regenerate source/server/pkg/proto bindings; it requires protoc and uses Go plugin versions v1.36.11 and v1.6.2, installing missing plugins. The documented server build command is `cd source/server && go build -o bin/cercano ./cmd/cercano/`. No build or generation was run during this recovery baseline.

Read the taxonomy reproduction and routing.go directly. Confirmed the root cause against the fresh failing test output: TaskAssignment defaults all non-dispatch tasks to Primary/Premium, while ValidateRouting and SetTaskAssignment accept only chat and dispatch. ResolveTask applies explicit quality only to dispatch.

Inspected dispatch/destination_test.go fixtures covering independent cloud backup chains, missing Secondary failure, legacy co-processor preservation, explicit model overrides, and prepared targets. Located existing fixture entry points in config/routing_test.go (sparse assignments, references, persistence/clone), routingwire/routing_test.go (snapshot, presence, invalid graph), agentclient/routing_test.go (presence), server/routing_settings_test.go (atomic presence), and CLI cloud_routing_test.go (draft/save/navigation behavior). These are reusable baseline seams, not evidence that the new taxonomy or redirects work.

Fresh direct executions from source/server:

- `go test ./pkg/config ./internal/dispatch ./internal/routingwire -skip TestTaskTaxonomy -count=1`: exit 0; all three packages pass.
- `go test ./pkg/config -run TestTaskTaxonomy -count=1`: exit 1, expected; all seven new defaults and assignment setters still fail. No production fix has been applied.

Next in Phase 1: finish the scoped caller/wrapper inventory without truncated evidence, remaining contract reproductions, and affected-package baselines before Phase 2. The reproduction remains intentionally failing; this recovery does not claim implementation completion, full regression coverage, or live inference verification.

## Caller inventory and dispatch reproductions — 2026-09-12

Completed the scoped caller inventory in caller-inventory.md, using bounded delegated searches and direct wrapper inspection. Located 12 production dispatch.Spec constructors in 10 files, the workflow alias, research budget/execution wrappers, the shared worker tool service, and the agent guidance updates required. This is not a runtime propagation claim.

Added internal/dispatch/task_taxonomy_repro_test.go. The delegated first draft used nonexistent imports/types/signatures; the initial compile failure was a test-authoring error, not a product failure. Corrected the fixture against existing destination_test.go and engine signatures before recording behavioral evidence. Added explicit cloud flags and a positive TaskDispatch control. A delegated review recommended explicit Mode/Role in the unknown-class case; those are now supplied.

Behavioral results:

- Explicit TaskDispatch honors the saved Secondary/Standard assignment: PASS.
- Missing-class RoleMain dispatch should honor that same bucket: FAIL; Target resolves legacy model and Dispatch calls Primary rather than Secondary.
- Unknown explicit routing class should reject before inference: FAIL; Target and Dispatch accept it and Dispatch calls Primary.
- Legacy co-processor stays Local with its legacy model: PASS. This is a baseline guard, not complete exclusion coverage after redirects.

Root cause: Engine.resolve branches on nonempty RoutingTask without validating it. Empty RoutingTask uses role policy and never consults the saved dispatch assignment. Config.TaskAssignment returns Primary/Premium for unknown tasks. No production fix was applied.

Fresh package baseline command from source/server:

`go test ./pkg/config ./internal/dispatch ./internal/routingwire ./internal/inference ./internal/worker ./internal/capabilities/builtins -skip TestTaskTaxonomy -count=1`

All six packages pass. From source/clients/cli, the five existing cloud-routing draft/navigation tests pass using `go test ./internal/ui -run 'Test(CloudChoicesStayDraftUntilSave|CloudRowNavigationRequiresConfirmation|CloudTabCloseRequiresConfirmation|RoutingDraftAndLocalCatalog|CloudPickerCancellationDoesNotChangeDraft)$' -count=1`.

Remaining Phase 1 work includes redirect/cycle reproductions and stronger excluded-producer/draft-ownership contract coverage. The new dispatch regression file is intentionally red on the current implementation; this checkpoint is a reproduction checkpoint, not a green production change. No live inference, UI interaction, full-suite run, or push was performed.

## Direct test-only continuation — 2026-09-12

The user authorized direct work after two more delegated attempts failed validation without verified writes or executions. No production code was changed.

Added builtins/task_taxonomy_exclusions_test.go: real runCoproc and Local producers retain their existing role, quality and explicit local model intent. Extended dispatch/task_taxonomy_repro_test.go with five engine-boundary controls for coprocessor, default/explicit local offload, and default/explicit watchdog-shaped specs. Target and Dispatch retain Local and the legacy quality/model despite Default dispatch being saved as Secondary/Standard. Watchdog specs mirror the inspected host and worker constructors; the tests do NOT invoke those constructors. The empty RoutingTask assertions record current producer behavior, not a requirement to retain that representation when explicit exclusion handling is implemented.

Added CLI ui/task_taxonomy_routing_repro_test.go: the dedicated Routing page is absent, and Cloud still renders ten routing controls. These are intentionally failing ownership reproductions, not full independent-draft Save/Discard coverage.

Fresh commands/results:

- From source/server: `go test ./internal/dispatch ./internal/capabilities/builtins -run TestTaskTaxonomyExcluded -count=1` — PASS both packages.
- From source/server: `go test ./pkg/config ./internal/dispatch -run TestTaskTaxonomy -count=1` — expected FAIL: all seven class defaults/setters, missing-class dispatch, and unknown explicit invocation remain broken; controls pass.
- From source/clients/cli: `go test ./internal/ui -run TestTaskTaxonomy -count=1` — expected FAIL: missing Routing tab and ten routing controls still owned by Cloud.
- From source/clients/cli: `go test ./internal/ui -run 'Test(CloudChoicesStayDraftUntilSave|CloudRowNavigationRequiresConfirmation|CloudTabCloseRequiresConfirmation|RoutingDraftAndLocalCatalog|CloudPickerCancellationDoesNotChangeDraft)$' -count=1` — PASS existing five draft/navigation controls.

Remaining Phase 1 gap: configuration has no destination-redirect field or resolver; a focused search of config Go files and agent.proto found no routing redirect API. The spec defines behavior but not storage field names or a resolver signature. No invented JSON schema, test-only redirect implementation, or skipped pseudo-reproduction was added. Direct directions, chains, cycle rejection, and exclusions under active redirects require the shared redirect API contract before executable tests can be written. Independent Routing/Cloud draft behavior and actual watchdog-producer integration coverage also remain outstanding. Phase 1 is not complete. No live inference, full-suite verification, build, or push was performed.

## Approved redirect schema reproductions — 2026-09-12

The user approved two scalar fields (`secondary_redirect`, `local_redirect`) rather than a map. Added compiling config tests for all four direct edges, both chains, omission, cycles, self/unknown targets, nonmutating validation, clone/YAML persistence, and sparse omission. `go test ./pkg/config -run TestDestinationRedirect -count=1` failed before implementation: all nine resolver cases report the missing resolver; all five invalid configurations are accepted; both serialized fields are lost. This closes Phase 1's missing redirect-contract reproduction gap. Redirect-aware exclusion integration and independent UI drafts remain later-phase verification, not established by these tests.

## Redirect configuration and transport implemented — 2026-09-12

Implemented approved SecondaryRedirect/LocalRedirect scalar fields with sparse YAML tags. Config.ResolveDestination validates the complete two-edge graph before following redirects; Primary has no redirect, unknown sources/targets and self/cyclic edges fail. ValidateRouting invokes the same graph validation. Resolution leaves assignments, quality and profile bindings untouched; it does not introduce fallback or enforce execution locality (those belong to later integration).

Extended RoutingAssignments protobuf with fields 6/7, routingwire encoding/application, and agentclient scalar cloning/conversion. Regenerated bindings via `bash source/proto/generate.sh`. Existing complete-draft validation now rejects redirect cycles atomically, and the existing worker RoutingSnapshot path carries both fields and all referenced Primary/Secondary profiles and backups without new credential fields.

Verification:
- Pre-implementation failures recorded immediately above; the runtime interface assertion in the reproduction was replaced by a direct resolver call after implementation.
- `go test ./pkg/config ./internal/routingwire ./pkg/agentclient ./internal/server ./internal/worker -run TestDestinationRedirect -count=1` — PASS all five packages.
- `go test ./pkg/config -run 'Test(DestinationRedirect|Routing|ResolveCloudModelForTier)' -count=1` — PASS, including real Save/Load and clearing persistence.
- `go test ./internal/routingwire ./pkg/agentclient -count=1` — PASS both complete packages.
- `go test ./internal/server ./internal/worker -run 'Test(DestinationRedirect|RoutingSettingsAtomicPresence|SnapshotConfigRoundTrip)' -count=1` — PASS.
- CLI's same five existing draft/navigation controls — PASS after the transport change.

The seven task classes, runtime destination selection, redirected model/credential/fallback verification, and dedicated Routing UI are NOT implemented in this unit. Known intentionally red taxonomy/dispatch/UI tests remain outstanding; no full-suite pass, binary build, live inference or interactive smoke check is claimed. Phase 2 remains partial. The next task is shared taxonomy metadata/defaults and validation.

## Shared taxonomy metadata and defaults — 2026-09-12

Read-only delegated repository inspection reported main at 6ed0e594 with no overlapping routing.go changes; untracked spec and initial config reproduction were preserved. The delegated implementation failed before inspection/edits; proceeded directly under the user's prior authorization.

Re-ran `go test ./pkg/config -run TestTaskTaxonomy -count=1` before implementation: all seven defaults and setters failed (Primary/Premium fallback and unknown-task errors). Added seven stable Task constants, centralized ordered TaskDefinitions metadata (returned by copy), and shared ValidTask validation. Config assignment defaults, saved-key validation, and setters now use those definitions. Unknown identity lookup returns an empty assignment rather than silently acquiring Primary intent. Explicit dispatch quality applies to the seven classes as well as Default dispatch; Chat retains its existing behavior. No runtime selection or UI code was edited.

Added tests for exact metadata/defaults, copy isolation, sparse per-field overrides, reset, quality/destination independence, invalid update nonmutation, clone and Save/Load. Existing task_taxonomy_repro_test.go was not edited and now passes. Added class-preserving client, protobuf, server settings and worker snapshot tests, including unknown-key atomic rejection and clear-to-default behavior.

Fresh verification:
- `go test ./pkg/config -count=1` — PASS full package.
- `go test ./internal/routingwire ./pkg/agentclient -count=1` — PASS both full packages.
- `go test ./internal/server ./internal/worker -run 'Test(TaskTaxonomy|DestinationRedirect|RoutingSettingsAtomicPresence)' -count=1` — PASS.
- `go test ./internal/dispatch -run TestTaskTaxonomy -count=1` — FAIL only the known ordinary missing-class Default dispatch reproduction (legacy/Primary used instead of Secondary/Standard).
- `go test ./internal/dispatch -run 'TestTaskTaxonomy(Unknown|Excluded|Explicit)' -count=1` — PASS. Unknown class no longer obtains a valid destination from config. Explicit boundary validation still needs review because injected assignment callbacks can bypass config; no claim of complete invocation-boundary enforcement.

Phase 2 remains partial; next is explicit invocation-boundary validation and remaining settings/snapshot acceptance review. Redirect-aware runtime integration and Routing UI remain pending. No build, live inference, interactive smoke check or full server suite was run.

## Explicit invocation identity validation — 2026-09-12

Added TestTaskTaxonomyUnknownRejectedBeforeAssignmentCallbacks covering candidate TaskFor and engine assignment callbacks across Target, PreparedTarget, one-shot Dispatch and agentic Dispatch. Before the fix all eight cases failed: invalid identity reached the callback, budgeting succeeded, one-shot inference reached the fake provider, and agentic execution reached runner-availability checking. Added shared engine.resolve validation using config.ValidTask before assignment callbacks; invalid explicit classes now return an identifiable error without consulting those callbacks or invoking inference. Empty-class behavior is deliberately unchanged in this unit.

Verification in source/server:
- `go test ./internal/dispatch -run TestTaskTaxonomyUnknownRejectedBeforeAssignmentCallbacks -count=1` — eight failures before the guard.
- `go test ./internal/dispatch -count=1 -skip '^TestTaskTaxonomyMissingClassUsesDefaultDispatch$'` — PASS after the guard, including the eight new cases. The single known missing-class reproduction is explicitly excluded, not fixed or claimed passing.
- `go test ./pkg/config ./internal/routingwire ./pkg/agentclient -count=1` — PASS complete packages.
- `go test ./internal/server ./internal/worker -run 'Test(TaskTaxonomy|DestinationRedirect|RoutingSettingsAtomicPresence)' -count=1` — PASS.

The optional model-facing class selector remains a Phase 4 task; this validation secures the existing engine Spec boundary. Runtime redirect selection and ordinary missing-class routing remain Phase 3 work. No push, live inference, build or full-server test run.

## Watchdog normal task routing — 2026-09-12

User amendment supersedes the watchdog exclusion: Watchdog is a normal `watchdog` class defaulting to Local/Standard, subject to common routing rather than an exemption. Updated spec/plan/inventory. Delegated write attempt failed validation without performing edits; implemented directly under prior authorization.

Added host and worker producer integration tests driving a real watchdog Gate through fake dispatch providers. Before the change both default and saved-assignment cases failed in both packages: zero task-assignment lookups, and saved Secondary assignment still executed locally. Added TaskWatchdog to shared metadata, validation and defaults. Both producers now supply that class with no forced tier or ModelOverride. Removed obsolete host and worker model-selection helpers and replaced the old helper-precedence test with producer-level coverage proving a legacy watchdog.model pin cannot override task model selection. Shared metadata-driven config/client/transport/snapshot tests now cover Watchdog as well. No new exemption flags or routing branches were added.

An existing worker gate test exposed a deficient test seam: its engine had no model resolver and its snapshot had no Standard model. After classification it failed with `dispatch: selected task model unavailable`. Updated only the test seam to install workerDispatchModelFor and task assignment, and supplied a Standard model in enabled-test snapshots; gate block/allow tests pass. Removed obsolete synthetic watchdog-exclusion cases; co-processor and explicit local-offload controls remain pending their separate migration decisions.

Final verification (source/server):
- `go test ./pkg/config ./internal/routingwire ./pkg/agentclient ./internal/watchdog -count=1` — PASS complete packages.
- `go test ./internal/server ./internal/worker -run 'Watchdog|TaskTaxonomy' -count=1` — PASS, including actual producer default Local/Standard and saved Secondary/Premium execution with a legacy model pin present, and class transport tests.
- `go test ./internal/dispatch -count=1 -skip '^TestTaskTaxonomyMissingClassUsesDefaultDispatch$'` — PASS; the known ordinary missing-class regression is explicitly excluded, not fixed.

Limitations: global redirect runtime integration, ordinary missing-class routing, dedicated Routing UI, and deprecated co-processor migration/removal remain pending. Legacy watchdog.model storage/transport/settings cleanup is not part of this unit; the stored field no longer controls watchdog dispatch. No live inference, build, full server suite, or push.

## Shared runtime redirect selection — 2026-09-12

Rechecked the remaining Phase 2 snapshot/settings coverage and marked the existing implementation complete: snapshots retain both destinations and backups, redirects and all metadata-defined task overrides (including Watchdog); settings tests cover atomicity and resets. Focused config/routingwire/agentclient/server/worker routing tests passed. Existing credential transport behavior was not changed.

Started Phase 3. Two delegation attempts returned no substantive audit/edits; implemented directly. The host reproduction initially lacked fixture credentials; after adding them, `TestDestinationRedirectProviderGraph` failed with `redirect selection: destination local unavailable` despite Local→Secondary configuration. This confirms shared selection ignored the stored redirect.

Added `Tiers.ResolveDestination`, captured alongside TaskFor from the same host published graph or worker configuration. SelectDestination follows the shared config resolver before evaluating existing placement/provider policy. Original task assignments/quality remain intact; Selection reports the final actual provider destination. Both host and worker supply the callback. MainModel/PrimaryModel helpers now resolve effective destination, and worker Chat binds the model from the selected provider's tier-aware serving route (matching host behavior) rather than the source assignment's cloud profile.

Tests added:
- Inference: all four direct edges, both chains, disabled redirects, cycle/invalid-target rejection, unavailable endpoints without source fallback, and final locality prohibitions.
- Host: Local→Secondary Chat and then Local→Primary after rebuild; new unbuilt redirect settings cannot change the published graph's selected profile/model; saved task identity/quality retained.
- Dispatch: Target, PreparedTarget and actual one-shot Dispatch agree for Default dispatch and Watchdog, including explicit quality and enabling/disabling chained redirects between calls.
- Worker: snapshot→provider assembly→Chat execution for Local→Primary and Local→Secondary; local HTTP fixtures verify final profile model and credential, authentication failover to that destination's own backup model/credential, and no requests to the unused profile.

The first worker fixture asserted unused credentials were never fetched and failed. Inspection confirmed existing API-key provider construction eagerly builds both destination chains (subscription credentials are separately lazy). Removed that over-strong assertion, retaining checks on actual request endpoints and credentials; credential-loading policy was not changed by this unit.

Verification (source/server):
- `go test ./pkg/config ./internal/routingwire ./pkg/agentclient ./internal/server ./internal/worker -run 'Test(Task|DestinationRedirect|RoutingSettings|RoutingWire|RoutingAssignments)' -count=1` — PASS (Phase 2 check).
- `go test ./internal/inference ./internal/hostsvc/providers ./internal/runner ./internal/worker -count=1` — PASS complete packages.
- `go test ./internal/dispatch -count=1 -skip '^TestTaskTaxonomyMissingClassUsesDefaultDispatch$'` — PASS; known missing-class reproduction still excluded explicitly.
- `go test ./internal/server -run 'Test(DestinationRedirect|ProviderGraph|RoutingContract|TaskTaxonomy|Watchdog)' -count=1` — PASS.
- `go test ./internal/inference/profilechain ./internal/inference/resilience -count=1` — PASS complete packages.
- `go test -race ./internal/dispatch ./internal/server ./internal/worker -run '^TestDestinationRedirect' -count=1` — PASS.

Phase 3 remains in progress. Important follow-up: runner/core.go still bases cross-destination fallback eligibility on the ORIGINAL assignment; redirects ending at Primary need their effective destination carried through the main provider/usage wrappers without rewriting saved task intent. Classified dispatch startup fallback is also still suppressed as before. This unit verifies redirect selection and within-destination backup chains, NOT full redirected cross-destination fallback parity. Other pending work includes ordinary missing-class defaults, model/context evidence matrix, complete host/open-model snapshot consistency, agentic redirect coverage, deprecated co-processor migration, and UI cleanup. No live cloud inference, production settings mutation, full-server suite, or push.

## Effective-destination fallback — autonomous continuation 2026-09-12

User authorized direct execution when delegation fails. Runner regression observed three wrong outcomes before the fix: Secondary→Primary and Local→Primary denied Local fallback, while a provider scoped to final Secondary inherited fallback from its original Primary assignment. Added immutable PolicyDestination on Selection and WithTaskRoute/TaskDestination on the existing assignment wrapper, forwarded through usage recording. Host and worker capture the final policy from selection without another redirect lookup; original assignments remain unchanged. Runner now gates fallback using this metadata, retaining legacy behavior for providers lacking it.

A classified startup regression also failed before the fix: Secondary→Primary under open_primary never attempted cloud on typed local startup failure. Classified final Primary now inherits existing Primary startup policy; final Local and Secondary still cannot cross. Existing cancellation, opened-stream and visible-output/tool replay guards remain unchanged.

Verification: complete inference, usage, hostsvc/providers, runner and worker suites PASS; dispatch suite PASS with only the known ordinary missing-class reproduction explicitly skipped; focused server DestinationRedirect/RoutingContract/ProviderGraph/Watchdog tests PASS. Actual host/worker handoff tests assert the metadata survives wrappers. No live inference or push. Remaining runtime work includes missing-class default, producer classification, broader evidence and snapshot validation.

## Caller classification and dispatch selector — 2026-09-12

Regressions observed explicit dispatch classes ignored, Review missing its class, and both Research budget/execution specs missing their class. Added shared-metadata enum `class` to dispatch/workflow arguments with unknown-class rejection; omitted class stays Default dispatch and explicit difficulty remains quality-only. Review (one-shot/agentic) and Git land now use their configured classes without fixed quality overrides. Research (including budget and query-generation paths) carries Research; query-only DisableThinking is preserved.

Migrated text-analysis helpers to Reconnaissance and renamed the common implementation runTextAnalysis. Kept existing capability names, removed their deprecated co-processor routing path. Next-action extraction uses Reconnaissance. Added routing_task to ProcessRequestRequest and mapped it through agent Request. MCP Research passes Research, project-context extraction passes Reconnaissance, documentation generation passes Mechanical development. Removed unused grpcModelCaller implementation. The legacy coproc wire flag is deprecated but translates to ordinary Default dispatch, not a separate policy. Explicit local_offload remains unchanged pending its explicit intent representation in the missing-class slice.

Tests formerly testing Coproc placement now explicitly assign Default dispatch to Primary to test Primary locality/fallback behavior; their cloud_primary expectation now selects cloud. Separate new coverage proves unconfigured legacy one-shot uses Secondary/Premium, not the retired local preference. A pre-existing MCP test mock lacked UpdateRoutingAssignments and was repaired to return Unimplemented for the unused method, permitting package tests to compile.

Verification: full builtins, MCP, agent, and persistence suites PASS. Focused server Query/Research/TaskClass/RoutingContract/DestinationRedirect tests PASS. Added explicit selector, Review and Research producer regressions and legacy default test all PASS. Protobuf regenerated via source/proto/generate.sh. No live inference, settings changes or pushes. The untracked efforts/token-accounting/spec.md belongs to unrelated work and is excluded from checkpoints.

## Missing-class Default dispatch — 2026-09-12

The previously skipped reproduction failed with Target=legacy and a Primary call instead of the saved Default dispatch Secondary/Standard target. Engine resolution now normalizes ordinary empty class to Default dispatch. Dispatch passes the normalized class to agentic execution and fallback; source strings and legacy Role do not infer a class. Explicit LocalOffload intent is set only by the local capability to retain its pre-existing model/placement policy; it cannot be combined with a task assignment. No Watchdog or deprecated co-processor exemption was added.

Legacy engine payload/session/usage tests now explicitly request Chat/Standard or explicit local-offload intent instead of accidentally relying on an unclassified Role. Obsolete co-processor exclusion tests removed. New tests cover ordinary empty class for both legacy roles and misleading source labels, PreparedTarget, one-shot and agentic propagation, and conflicting local/class intent. Host startup/replay integration now exercises classified Primary under open_primary rather than retired Coproc routing.

Verification: complete dispatch suite PASS with NO skips; complete hostsvc/tools and builtins suites PASS. Worker, MCP, and agent suites also passed in the broader missing-class run (before final test-only corrections to dispatch/host fixtures). No live calls or push. Routing UI and final evidence/doc audit remain.

## Dedicated Routing page — 2026-09-12

Initial CLI reproductions failed: no Routing tab and Cloud exposed routing controls. Added independent Routing scope/tab, moving profile destination bindings, backups and task controls out of Cloud. All ten current TaskDefinitions (Chat, Default dispatch and eight classes including Watchdog) render metadata-driven destination, quality, effective destination and per-task reset controls. Light is the UI label for persisted economy; Watchdog inherits Local/Standard. Secondary/Local selectors restore own configuration with an empty override. Local setup points to Runtime / Local Models rather than inventing a cloud profile.

Cloud no longer exposes activate/backup or routing fields. Routing uses its own commit prefix and draft lifecycle. Save snapshots the draft, validates cycles before transport, retains edits on failure/reconnect, refreshes the saved baseline on success (including availability warnings), and never clears Cloud edits. Scoped navigation/discard respects draft ownership. Added eighth-tab navigation, effective-route rendering, reset/isolation, failed-save, disconnected-save and cancel-navigation tests. Existing narrow layout test caught overlong task labels; separate task headings and short field labels fix it.

Full CLI UI and wizard tests PASS. CLI build `go build -o /tmp/cercano-routing-cli .` PASS (an initial guessed ./cmd/cercano path failed; corrected to the module's actual root main package). No live/manual UI interaction claimed; no running service restarted. Ignored watchdog.model control was not present in the current UI; its transport/storage field remains compatibility-only, not a route selector.


## Final verification and closure — 2026-09-12

Completed snapshot hardening (captured mode/model/readiness, deep-cloned local model maps), selected-target attribution and a real client/settings→disk→worker snapshot→shared chain fake-provider integration test. See verification.md for exact commands and limits. Broad affected server packages and CLI UI/wizard tests pass; all-class quality/agentic matrix, final cross-layer test with race detection and both binary builds pass. No taxonomy failure is skipped.

An independent adversarial review found same-tab navigation could drop Routing edits; failing-before/passing-after regression added and fix verified by the complete CLI UI suite. Later review returned no fresh evidence; no exhaustive independent-review claim is made. Source audit confirmed class-bearing producer inventory; explicit-path checkpoints preserve concurrent token-accounting documentation. Manual UI/live inference/production credentials/runtime restarts were not performed. No push.
