# Runtime restart tool

`restart_runtime` restarts a managed **llama-server instance**, not Cercano's agent.

- The tool is registered and callable in normal and debug sessions.
- Its model-facing definition is advertised only after entering `/d`. The flag is per turn, passes through the worker boundary, and does not change permissions.
- Execution uses destructive tier X and the existing confirmation flow. Read-only capability profiles still apply.
- `instance_id` selects an exact managed llama-server instance. If omitted, selection succeeds only when exactly one llama-server instance exists. Otherwise the result lists eligible IDs and nothing is restarted.
- The host uses the existing `GetRuntimeStatus` and `RestartRuntime` RPC implementations. Workers forward the operation over their existing host connection rather than guessing an address or killing a process.
- Settings, context size, memory limits, and runtime guards are unchanged. There is no fallback to restarting the agent.

Example arguments:

```json
{"instance_id":"llama_server:example:12345"}
```

The JSON result reports success, runtime state, old PID, and the returned instance. A refused restart can leave the old process stopped: the tool checks status and reports that outcome along with the original error. An unrelated existing instance of the same model does not count as successful recovery. A failed status check reports an unknown outcome rather than claiming the runtime is stopped.

Already-cancelled operations do not issue a restart. Worker cancellation removes the pending reply waiter, and late replies are safely ignored. Once a restart has begun it is not automatically undone or retried.

## Verification

- Server and CLI builds and affected-package vet checks pass.
- Race-enabled server tests cover runtime orchestration, catalog visibility, capability registration, worker/host integration, runner, server, toolstack, and agentclient.
- Race-enabled CLI UI and slash-command tests pass.
- Tests cover explicit/sole/ambiguous selection, non-llama rejection, memory-guard and transport errors, stopped versus running/unknown outcomes, cancellation, debug-flag isolation between turns, and permission allow/deny in both modes.
- An existing `TestPendingCarriesPersist` scheduling failure was reproduced: it could resolve before its waiter registered. The test now waits for an accepted resolution; production permission behavior is unchanged. Twenty repeated race-enabled runs pass.
- Tests use fake runtime RPCs and loopback worker streams. No live runtime or agent restart was performed while implementing this feature.

The new protobuf fields are additive; rebuild the agent, CLI, and workers together to use the debug advertisement and runtime-control path. DeepInfra default-model work is separate and unchanged here.
