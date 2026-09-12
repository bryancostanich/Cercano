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
