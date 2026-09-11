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
