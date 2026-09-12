# Task taxonomy and destination routing — final verification

Date: 2026-09-12. Work completed on the explicitly authorized current branch, `main`; no push, merge, runtime restart, paid inference, or production configuration mutation.

## Implemented contract

- Shared task metadata contains Chat, Default dispatch, Reconnaissance, Mechanical development, Investigation, Implementation, Review, Research, Git land and Watchdog. Watchdog defaults to **Local / Standard**.
- Ordinary missing class uses Default dispatch regardless of legacy role, source label or prompt wording. Unknown explicit classes are rejected. Explicit difficulty changes quality only; Light is persisted as `economy`.
- Secondary and Local redirects follow their approved edges/chains before final destination selection. Saved assignments, quality and profile bindings are preserved. Cycles and invalid updates are rejected atomically.
- Host and worker candidate graphs carry assignment, redirect, mode and model-resolution information together. Local runtime model overrides are deep-copied by `Config.Clone`, preventing snapshots from sharing mutable maps.
- Effective policy destination travels through Chat and usage wrappers. Final Primary inherits its permitted fallback, including redirects ending there; final Secondary/Local do not acquire new fallback edges. Existing cancellation, authentication and visible-output/tool replay guards remain.
- Target, PreparedTarget and Dispatch agree. Budget targets preserve selected destination/profile metadata when a provider omits it, without overwriting authoritative serving-route metadata.
- Review, Research (including budget/query paths), Git land, host/worker Watchdog and the other audited producers carry explicit classes. Text-analysis helpers use Reconnaissance. The deprecated `coproc` wire flag translates to ordinary Default dispatch; no parallel co-processor or watchdog exemption remains. Explicit local-offload intent retains its prior policy.
- Routing owns destination bindings, backups, redirect selectors, all task rows, effective labels, inheritance and reset. Cloud owns profiles/credentials/model choices. Runtime/Local Models retain local model ownership. Save, Discard and navigation preserve independent drafts; same-tab selection no longer destroys unsaved edits.

## Reproductions and fixes

The chronological baseline contains the original failures. Final slices additionally reproduced:

1. A published provider graph used newly edited locality mode before rebuild (`destination secondary prohibited by open_only`). Mode now comes from the same candidate graph.
2. Model-budget target omitted profile/destination despite selecting Secondary. Selection metadata now supplies missing fields.
3. `Config.Clone` aliased local model override maps, so editing a clone changed the saved graph's Standard model. A minimal config regression and model-resolver snapshot test prove independence after the fix.
4. Independent review found that reselecting the current Routing tab rebuilt the page without an unsaved-edit prompt. A failing UI regression now passes after making same-tab selection focus-only.

Legacy payload/session/usage tests were updated to explicit Chat/Standard or explicit local-offload intent rather than relying accidentally on old empty-class role behavior. Separate ordinary-default regressions cover empty, Main and Coproc roles and misleading source strings. No failing taxonomy test remains skipped.

## Cross-layer evidence

`TestRoutingSettingsClientPersistenceSnapshotExecution` uses a real local gRPC client/server settings call, reads settings back through the client, saves/reloads a temporary configuration file, serializes/deserializes a worker protobuf snapshot, and executes the restored routing configuration through the shared destination-chain implementation with local HTTP fixtures.

It checks Watchdog's saved Local/Standard assignment redirecting to Secondary, with Default dispatch deliberately configured elsewhere; selected profile/model and context-window evidence; authentication failure and the final destination's own backup model/credential; actual returned backup profile/destination/model/context; no request to the unused source profile; and no fixture credentials in the config snapshot. The actual worker Main wrapper is covered separately by `TestDestinationRedirectWorkerExecution`, including both Local→Primary and Local→Secondary.

All task classes are exercised with inherited, Light, Standard and Premium quality through Target, PreparedTarget and agentic handoff. Other tests cover all redirect edges/chains, disabling redirects, invalid cycles, final availability/locality restrictions, settings refresh, unknown classes, invocation model overrides and replay protection.

## Commands and results

All commands below passed on the implementation described here. Server commands run from `source/server`:

```sh
go test ./pkg/config ./internal/routingwire ./pkg/agentclient \
  ./internal/openmodels ./internal/inference/... ./internal/dispatch \
  ./internal/usage ./internal/hostsvc/... ./internal/runner \
  ./internal/worker ./internal/server ./internal/capabilities/builtins \
  ./internal/mcp ./internal/agent ./internal/watchdog -count=1

go test ./internal/dispatch -count=1

go test -race ./internal/dispatch ./internal/server ./internal/worker \
  ./internal/runner ./pkg/config \
  -run 'DestinationRedirect|RedirectedChat|CandidateSnapshot|RoutingSnapshotLocal|AllTaskClasses|TaskTaxonomy|RoutingContract' -count=1

go test -race ./internal/server \
  -run '^TestRoutingSettingsClientPersistenceSnapshotExecution$' -count=1

go build -o /tmp/cercano-routing-server ./cmd/cercano
```

CLI commands, from `source/clients/cli`:

```sh
go test ./internal/ui ./internal/wizard -count=1
go build -o /tmp/cercano-routing-cli .
```

The broad affected-package server run reports 22 passing package results; focused tests added afterward were also run directly, including the all-class matrix and cross-layer fixture. Protobuf bindings were regenerated using `source/proto/generate.sh`. An initial CLI build attempt incorrectly guessed `./cmd/cercano`; the root-main command above is the verified build.

## Review and limitations

- Independent adversarial review identified the same-tab draft-loss defect, which was reproduced and fixed. A later review was limited and did not establish a broader independent audit. Direct source audits and regression tests supplement that review; no claim of exhaustive external review is made.
- Multiple delegated implementation attempts failed without edits. The user explicitly authorized direct work when delegation fails. Semantic plan-status updates and some repository audits succeeded. The final delegated whitespace check passed, but its refreshed changed-path artifact was not verified; final checkpoints use explicit known effort paths.
- No manual interaction with a running updated Routing/Cloud UI was performed. Automated UI tests cover rendering, narrow layouts, navigation, draft retention, reset, independent ownership and save failures. The running agent was not restarted to load the new binary/schema; the binaries were built to `/tmp` only.
- No live inference, real provider authentication, real-runtime model availability or interactive Git landing was exercised. Inference integration uses local fixtures. Deterministic Git mechanics were not changed.
- The entire server `go test ./...` and a repository-wide end-to-end suite were not run; verification is the broad affected-package set and targeted integration/race tests above.
- The legacy `watchdog.model` storage/transport field remains for compatibility but does not select Watchdog dispatch models. It was not exposed by the current CLI UI.
- Unrelated token-accounting documentation (`efforts/token-accounting/spec.md` and `docs/inference-token-accounting.md`) is excluded from effort checkpoints and preserved.
