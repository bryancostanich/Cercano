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
