# Final outcome — live developer reset

The user's final instruction explicitly superseded the offline-only design. All new lifetime/process locks and client-lifecycle restrictions were removed. The current command is `cercano reset --setup`: terminal confirmation, no session closure/drain/process checks, live agent config refresh when present, local reset otherwise. The user controls concurrent activity and accepts in-flight errors and stale writeback/refresh races. Historical lock tests below describe removed work, not the current feature or a remaining blocker.

Current implementation includes selective YAML reset, complete Cercano credential enumeration/deletion without reading secret values, current-default restoration, shared fresh wizard state, a confirmed ResetSetup RPC, and a terminal command that never silently falls back locally after a reachable-agent error.

The final live integration invokes the command's Perform path against a local gRPC agent with fake credentials while a second client connection remains open. It verifies that connection still works, the live routing graph/config are reset, old/orphan credentials are gone, history sentinel bytes survive and fresh wizard state has no old baseline. A separate llama-server catalog fixture verifies normalized-directory downloads remain marked Downloaded after reset and custom-directory model bytes remain unchanged. No inference or download occurs.

Additional failing-before/passing-after regressions cover a stale local TurnRunner surviving native provider removal, unknown agent outcomes being misleadingly printed as zero progress, and a wizard-path override colliding with the config path. YAML type errors were also sanitized to avoid echoing config-value snippets.

## Final checks

From source/server, all 11 affected packages passed:

```sh
go test ./internal/server ./internal/hostsvc/providers ./internal/hostsvc/config \
  ./internal/hostsvc/credentials ./pkg/agentclient ./internal/mcp ./cmd/cercano \
  ./internal/setupreset ./internal/setupresetcmd ./pkg/setupstate ./pkg/config -count=1

go test -race ./internal/setupreset ./internal/setupresetcmd ./internal/server \
  -run 'SetupReset|ResetSetup|ResetPreserves|ResetFailures|ResetRefuses|Confirmation|PartialFailure|UnknownAgent|WizardPath|ReachableAgent|LocalReset' -count=1

go test ./internal/localruntime/llamaserver \
  -run '^TestSetupResetRediscoversPreservedDefaultDownloads$' -count=1

go build -o /tmp/cercano-debug-reset ./cmd/cercano
```

From source/clients/cli, root, wizard and UI tests and the build passed:

```sh
go test . ./internal/wizard ./internal/ui -count=1
go build -o /tmp/cercano-debug-reset-cli .
```

Protobufs were regenerated via source/proto/generate.sh. The MCP test mock was extended for the new RPC without exposing a reset tool. Additional full consumer suites also passed: `go test ./internal/worker ./internal/runner ./internal/capabilities/builtins -count=1`, bringing affected server coverage to 14 passing packages.

Actual built-binary smoke checks used isolated HOME/XDG_CONFIG_HOME/TMPDIR/wizard paths: `reset --help` returned 0, `reset` returned 2, and noninteractive `reset --setup` returned 1 even with RESET piped in. All three left the temporary state directory empty. No confirmed production-adapter invocation was made.

## Review and limits

A bounded independent review inspected the core and found no concrete defect; it did not complete an exhaustive multi-file audit. Direct source review and regression tests cover command triggering, live routing, preservation and error reporting. The complete repository end-to-end suite was not run; checks above target affected interfaces and behavior.

No actual user config, OS-keychain credentials, sessions, runtime installations or model files were reset. Credential tests use fake/in-memory stores; gRPC integration is local and uses those stores. Real OS keychain deletion and manual interactive setup against a running user's installation were not exercised. Test binaries were built under /tmp, not installed, and no running agent was restarted. No push or merge is authorized/performed.

Unknown unrelated YAML keys are preserved. Unsupported aliases/merges and malformed config are rejected rather than silently losing unrelated data. External auth/environment/browser state is intentionally not cleared. In-flight errors, partial failure and stale writes from other sessions remain intentional limitations of this user-approved debug reset.

---

## Historical implementation record (superseded where noted)

# Setup reset verification — in progress

Implementation worktree: Cercano-new-user-reset, branch feat/new-user-setup-reset, base d9d21fd3. No real reset, user-config mutation, credential reads/deletes, service shutdown, inference or model-file deletion performed.

## Baseline and pure configuration slice

Baseline `go test ./pkg/config ./internal/secrets ./internal/hostsvc/credentials ./internal/chatgptauth ./internal/anthropicauth -count=1`: all five packages passed (temporary/in-memory fixtures). Log: /tmp/cercano-reset-baseline-server.log.

Added ResetSetupYAML with explicit root/nested ownership. Unknown unrelated YAML keys and feature preferences survive; complete setup-owned subtrees restore current Defaults; omitted legacy defaults are removed. Conservatively rejects aliases/merges, duplicate/non-string keys, multiple documents, malformed known types and non-mapping roots. Formatting can normalize. It has no filesystem/keychain dependencies.

New API tests initially failed compilation before implementation. Complete pkg/config suite then passed. Additional parent audit reproduced yaml.TypeError echoing `private...` from an invalid config field, which could expose credential fragments. Type-validation errors are now sanitized rather than including the parser's input snippets. Regression and complete pkg/config suite passed after the fix (latest run 1.644s).

Inventory records remaining coordination and wizard boundary work. Command, orchestration, credential clearing and process coordination are not yet implemented or verified. No whole-feature completion claim.

## Shared wizard persistence slice

Extracted the single persisted schema/path/Load/Save/Clear contract into server/pkg/setupstate. CLI wizard.State is a defined wrapper over the shared underlying State, preserving CLI navigation methods without duplicating fields; nested snapshots/steps are aliases. UI navigation remains in CLI. Fresh creates only the first locus step and no baseline/answers. Startup now uses a testable needsSetup helper with unchanged ordinary semantics.

Tests were added before APIs existed (compile-red), then implemented. pkg/setupstate tests PASS; complete CLI root, internal/wizard and internal/ui suites PASS. Existing persisted YAML including a StepDone summary and profile choice snapshots round-trips unchanged; fresh persistence removes old rollback data. Fresh state with a retained config triggers setup, clearing completed state stops reopening, and the config bytes remain unchanged.

Server and CLI builds PASS to /tmp/cercano-reset-stage3-server and /tmp/cercano-reset-stage3-cli. Neither binary was launched. Actual download rediscovery after orchestration remains a later integration check, not claimed complete here. The existing non-atomic wizard Save behavior is unchanged by extraction; reset orchestration will publish its prepared state atomically.

## Cooperative lifetime coordination — verified, legacy boundary pending

Added pkg/statelease with nonblocking shared participant and exclusive reset flock leases, no-follow private regular lock-file validation, current-owner checks, hardlink rejection and OS crash release. User-wide default root is ~/.cercano/state, not a configurable config-file path, because the keychain namespace is user-wide. Unsupported platforms explicitly reject reset; normal app startup retains its previous behavior there.

Agent/server and CLI entrypoints now retain a strongly referenced participation lease until OS exit (not a deferred early Close). Client Dial/DialExisting retain a lease through Close and any in-flight reconnect. Closed clients cannot restart reconnect. Autolaunch lock acquisition is now cancellation/deadline aware. Reset command dispatch must be installed before the ordinary process lease in the later command slice, so it can acquire exclusive access rather than conflict with itself.

New API tests were compile-red before implementation. A never-dialed closed client initially reentered reconnect and returned context.Canceled; the regression now requires errClientClosed and passes. Subprocess tests cover simultaneous participants, reset/participant mutual exclusion, parallel reset refusal, process-exit and killed-test-child release, strong process-reference retention after GC, and unsafe lock paths. No actual agent subprocess or real credential store was used.

Latest verification: go test -race ./pkg/statelease ./pkg/agentclient -count=1 PASS; focused server cross-layer settings fixture PASS; complete CLI root/wizard/UI suites PASS; server, alternate agent and CLI builds PASS. Windows cross-build of pkg/statelease PASS with explicit unsupported-reset behavior. Integration tests that use DialExisting now isolate HOME; autolaunch-lock tests also isolate TMPDIR. No production binary was launched.

Blocking question before credential deletion/orchestration: updated cooperating binaries can be excluded reliably, but arbitrary older/renamed binaries cannot honor a new lock. Known canonical legacy processes can be detected and refused, but that cannot prove the absence of every unrecognized old writer. Parent is requesting explicit approval of a supported-updated-binaries concurrency contract plus conservative legacy detection and a requirement to close older builds. No deletion implementation has been added while this boundary remains unresolved.
