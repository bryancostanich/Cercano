# Execution investigation — 2026-09-10

Implementation worktree: Cercano-automatic-model-context-sizing, branch feat/automatic-model-context-sizing, initial HEAD 5a7e4c81. The approved spec/plan and task statuses are currently stored in the source worktree at /Users/bryancostanich/git_repos/bryan_costanich/Cercano/efforts/automatic-model-context-sizing.

## Observed regression

Added TestSavePreservesAutomaticLlamaServerContext in source/server/pkg/config/config_test.go. Command from source/server:

```
go test ./pkg/config -run '^TestSavePreservesAutomaticLlamaServerContext$' -count=1
```

Expected failure observed:

```
saving automatic context introduced an explicit context_size override
automatic context became explicit after save/reload
```

This is deliberately a RED regression checkpoint, not a production fix or a passing test gate.

## Instructions read

AGENTS.md, CLAUDE.md, docs/agent/self-dev.md, and introductory sections of docs/agent/README.md and docs/features/cli/README.md. Self-dev requires delegation for mechanical work and recommends module make build targets for signed binaries. No live binaries have been rebuilt or installed.

## Concrete interface findings

- pkg/config/config.go Config.Clone copies nested structs and explicitly clones llama-server slices. A new ContextSize pointer must be independently cloned.
- internal/hostsvc/config Get and Set use Config.Clone; adding the pointer clone preserves their isolation guarantees.
- internal/localruntime/llamaserver/provider.go ReloadConfig assigns withDefaults(cfg.LlamaServer); snapshot returns a by-value copy. Pointer ownership needs attention at this additional boundary.
- Provider.Start performs adoption/reuse before new-process memory checking. New confirmation state must cover adoption and reuse as well as fresh startup.
- Start calls checkMemoryBudget, then startProcess. Existing context resolution is repeated in checkMemoryBudget and launch-argument construction rather than pinned once per launch.
- waitReady checks /health; Start later marks the record running. finishReadiness is a separate delayed-ready path. Both require capacity confirmation.
- kvEstimate returns zero on missing path, failed open/parse, or unsupported KVBytesPerToken. Automatic sizing cannot treat these silent zero estimates as evidence of memory safety.
- Existing argument construction strips --ctx-size from config/model extras; aliases such as -c and environment precedence still require a full audit.
- Independent local context consumers include cmd/cercano startup summary/chat budget callbacks, cmd/agent, internal/server configuration closures, internal/modelwindow, runner, toolstack, and persistence. This is not a single meter-only fix.

## Installed runtime contract, directly observed

Read-only `/opt/homebrew/bin/llama-server --help` reports --ctx-size default 0 (loaded from model), --parallel default -1 (automatic), and unified KV enabled when slots are automatic.

Read-only HTTP probes of the existing runtime at 127.0.0.1:58586:

- /props: default_generation_settings.n_ctx = 131072; total_slots = 4.
- /slots: all four slots independently report n_ctx = 131072.

Consequently, dividing the properties value by total_slots would be wrong for this installed runtime. Do not infer a universal layout from these observations: fixture tests must cover the supported response contracts and fail closed when per-request capacity cannot be confirmed.

## Historical execution blocker (before parent-agent rebuild/restart)

Read-only small delegations sometimes work, but implementation delegations continue to fail dispatch preflight against a reported 8192-token context, despite the directly confirmed 131072 runtime window. Narrowing to a single Bash tool and a short plan reference did not unblock Phase 2. A prior write-granted delegation returned only a generic tool-used statement; direct inspection confirmed it had not added the regression, so the small single-file regression was added and executed directly.

This demonstrates dispatch/runtime accounting disagreement. Stale parent-agent config is a hypothesis, not yet proven. Restarting the llama-server child has not corrected dispatch accounting. Further parent-agent restart/config mutation is outside the current execution authorization and must not be performed silently.

No production fix, live configuration modification, additional runtime restart, or push occurred during this execution attempt. Remaining Phase 1 work (including stale-config service regression and complete consumer mapping) is incomplete; Phase 2 implementation has not started.

## Post-agent-restart verification and service regression

After the user rebuilt/restarted the parent agent, a delegated Bash invocation successfully executed the config regression and reported its expected assertion failures, without preflight overflow. Multi-step delegations still sometimes return only a tool-used statement; their work is not assumed complete. Direct inspection is required.

Added TestLocalContextWindow_ConfigEditDoesNotChangeServingCapacity at the runner/config-service seam. Exact command from source/server:

```
go test ./internal/runner -run '^TestLocalContextWindow_ConfigEditDoesNotChangeServingCapacity$' -count=1
```

Observed failure: config-only edit changed serving budget from 65536 to (8192, true) without a runtime restart. This is NOT a live-instance integration test. Once runtime-owned capacity is available, the test must inject a confirmed instance and retain separate lifecycle integration coverage.

### Verified consumer map

- cmd/cercano/main.go: setup Save calls at 1291 and 1341; legacy key migration Save at 684; applyLlamaServerSetupDefaults at 1422 fills absent context; startup summary window at 379 and chat budget at 487; dispatch callback at 711.
- internal/server/server.go: NewServer SetContextWindowResolver callback at 1062 reads cfgSvc.Get every call, independently of runtime state; persistConfig and UpdateConfig invoke shared persistence.
- internal/runner/core.go: knownContextWindowFor reconstructs local capacity from config via modelwindow.LocalRuntimeWindow; returns window>0 as known.
- internal/toolstack/toolstack.go: InstallCapabilities DispatchTarget independently resolves local window from its captured Config pointer. Research budget callers consume this target seam.
- internal/hostsvc/tools/tools.go: SetContextWindowResolver supplies the ToolLoopInput context fields in dispatch.
- internal/hostsvc/persistence/persistence.go: resolveWindow independently resolves local config capacity, then falls through cloud evidence and family-based meter resolution.
- cmd/agent/main.go: local callback directly returns llama config size.
- internal/worker/worker.go: Deps supplies CloudContextWindow from copied evidence. Internal runner local path still relies on copied Config. internal/worker/model_metadata.go transports general provider metadata; local instance-bound confirmation is not currently wired through it.
- internal/modelwindow/modelwindow.go: MeterWindow and LocalRuntimeWindow/LlamaServerWindow are config/catalog reconstruction seams, not live-instance observers.

### Launch and memory details

startProcess calls argsFor(p.snapshot(), instance.model, port), independently of checkMemoryBudget's earlier snapshot, so the same launch must instead retain one resolved size. cmd.Env is unset and inherits the environment; context-affecting environment variables and flag aliases must be normalized or rejected. argsFor strips only the long --ctx-size form from extras, so -c requires explicit handling.

GGUF ParseMeta requires core sizing fields including ContextLength; its KVBytesPerToken uses an f16 assumption. The existing kvEstimate silently returns zeros for parse failures and zero estimates. The approved implementation must fail closed for automatic sizing lacking required evidence, validate arithmetic bounds and cache/parallel flags, and avoid describing unsupported inputs as safely estimated. Existing Start reuse/adoption and finishReadiness paths must confirm capacity too.

No evidence found in this audit invalidates the approved optional-config/runtime-owned approach. Unknown metadata is an actionable failure case already covered by the spec. Non-default launch settings that make the memory estimate untrustworthy must not be silently accepted by the automatic path. Full behavioral verification belongs in the planned resolver, runtime, and consumer integration phases.

## Phase 2 implementation verification

Implemented optional llama-server ContextSize (*int, YAML omission), removed the 8192 allocation and ContextSizeSet parser, removed both config/setup and runtime-detection default filling, and added positive-value validation at Load/Save/Set/Mutate boundaries. Set and Mutate now return errors; invalid mutations retain the previous state. Provider construction, reload, and snapshots deep-copy context pointers.

Tests now cover null/absent repeated save/reload, unrelated settings saves, deliberate 8192 and 65536 overrides, nonpositive rejection, setup save/reload, config service snapshots, and provider snapshots. Mutate invalid-state regression was observed failing before the atomic candidate-validation fix.

Passed:
- go test ./pkg/config ./internal/hostsvc/config ./internal/localruntime/llamaserver ./cmd/cercano -count=1
- go test -race ./pkg/config ./internal/hostsvc/config ./internal/localruntime/llamaserver -count=1
- go test ./internal/modelwindow ./internal/hostsvc/persistence ./internal/server ./internal/worker ./internal/toolstack -count=1
- go test ./... -run '^$' (server compile-only gate, not full test suite)
- go test ./internal/ui ./internal/uiconfig -run '^$' (CLI compile-only gate)

The runner suite still fails the deliberately red TestLocalContextWindow_ConfigEditDoesNotChangeServingCapacity: 65536 becomes 8192 after config-only change. Runtime-owned confirmation and budgeting propagation are NOT implemented by Phase 2. Existing argument construction can omit a context flag for an automatic model without catalog policy; the planned safe GGUF resolver and memory gate in Phase 3 must address this before the full effort is ready to deploy. This checkpoint is an intermediate implementation unit, not completion of the six-phase contract.

No live config edits, runtime restarts, installations, or pushes were performed.

## Completed runtime-owned context implementation

Implementation checkpoints:
- b831ed82ead8: instance-bound planned/confirmed capacity and runtime/protocol/client publication.
- 44f52ac2194e: engine preparation, runtime-only budgeting, worker capacity/accounting transport, durable meter identity, UI and contract tests.
- ff5856428134: preserve the pre-existing non-llama-server window policy, with an observed-failing compatibility regression.

After the user asked to stop dispatching, all remaining implementation, probes, review, tests, and builds were performed directly. No subsequent delegation or independent model review was run. The adversarial contract review was performed directly by trying to refute readiness, generation binding, accounting transport, and compatibility guarantees; it was not an independent review.

### Direct review findings resolved with regressions

- A running endpoint without confirmation was previously accepted by the engine. The engine now refuses it before inference.
- A failed confirmation retained previously authoritative capacity. Confirmation invalidates the old value before querying.
- Cancellation could start a runtime before returning failure. Canceled preparation now exits before startup.
- A direct completion could cross a process generation after its request was prepared. Both direct and LLM-provider paths check the prepared identity before HTTP.
- A late readiness callback could promote an instance after its confirmation was invalidated. Promotion now requires valid confirmation.
- Worker inference already had a proxy, but request-accounting records were not forwarded to the host. A typed accounting message now carries the request's model, provider, generation, window and numeric costs.
- The meter's durable snapshot could label a fallback request with the primary model and could retain the previous process's capacity. Snapshots now store the request model/provider/generation; local meter reads use current confirmed capacity and mark old-generation usage stale.
- Removing managed context reconstruction also changed a non-managed backend's legacy explicit-window policy. That unrelated behavior was restored and tested.

### End-to-end fixture evidence

TestAutomaticConfigToLaunchConfirmationAndBudget exercises real config Save/Load, model metadata parsing, the memory guard, child launch arguments, HTTP props/slots confirmation, engine preparation, and tool-loop request accounting. It starts a small HTTP fixture process, not a real llama-server/model. Cases cover automatic native 2048 and explicit 8192/65536, each overriding a deliberately stale caller-supplied window of 131072. The native fixture's KV estimate is 192 MiB; mocked total memory is 64 GiB with 1 GiB already resident. No GPU/model simulation or live runtime deployment was performed.

The first broad parallel package run hit the fixture's original five-second startup deadline for one explicit case; nine isolated cases then passed. The fixture-only deadline was increased to 20 seconds, cleanup was moved ahead of startup so error paths cannot leak test processes, and startup diagnostics were added. Subsequent parallel package and race gates passed. The exact cause of the delayed startup was not established; no production startup timeout was changed for this test issue.

### Final verification

Passed server gate (from source/server):

```
go test ./pkg/config ./internal/localruntime/... ./internal/engine/llamaserver ./internal/conversation ./internal/hostsvc/... ./internal/runner ./internal/worker ./internal/agent ./internal/dispatch ./internal/toolstack ./internal/requestassembly ./internal/modelwindow ./internal/usage ./internal/server ./pkg/agentclient ./cmd/cercano ./cmd/agent -count=1
```

Passed CLI gate (from source/clients/cli):

```
go test ./internal/ui ./internal/uiconfig ./internal/wizard -count=1
```

Passed shared-state race gate:

```
go test -race ./pkg/config ./internal/hostsvc/config ./internal/localruntime/... ./internal/engine/llamaserver ./internal/conversation ./internal/worker ./internal/agent ./internal/runner ./internal/dispatch ./internal/toolstack ./internal/usage -count=1
go test -race ./internal/hostsvc/persistence -run 'Test(MeterUsesServingModelAndRejectsStaleGeneration|ResolveWindow)' -count=1
```

After the compatibility correction, modelwindow, runner, persistence, toolstack, server and cmd/cercano tests were rerun successfully, as was the modelwindow race test. The server-wide compile-only gate also passed during interface integration.

The additive SQLite migration was tested against a database lacking the new provider/instance columns, including preservation of existing rows and reopen/round-trip of new identity. Worker capacity and accounting were exercised through protobuf marshal/unmarshal fixtures. Runtime properties tests cover single-slot and unified four-slot responses, missing/malformed/contradictory evidence, and replacement-generation rejection.

Builds passed using the repository targets:

```
make build VERSION=context-sizing
make agent
make -C ../clients/cli build VERSION=context-sizing
```

The unified server and standalone CLI build outputs were code-signed by the repository build scripts. These are worktree-local build artifacts, not installations.

### Limitations and boundaries

- A broader race run including all persistence tests reported unsynchronized accesses to viewportResumeFakeStream.events in internal/hostsvc/persistence/resume_stream_test.go. The affected context-specific race tests pass. The viewport fixture was left untouched; no claim is made that the entire repository race suite is green.
- Full live llama-server/model integration was not run. The end-to-end process is a controlled HTTP fixture and the memory probes are mocked.
- No independent reviewer was used after the user's no-dispatch instruction; direct adversarial review and regression tests supplied the review evidence.
- No live config or conversation database was edited. No live agent/runtime was restarted, no binary was installed, and nothing was pushed.
- Generic 8K substitutions were audited in managed context resolution, detection and launch code; none remain there. Intentional model-specific 8K policies and the unrelated output-token budget remain intact.
- Historical explicit context_size values are preserved. Users must remove a value manually only if they know it was accidentally persisted by an older version.

The approved source spec/plan are retained at the original effort path. Final copies are included in this feature worktree so the branch carries its acceptance criteria and completion record.
