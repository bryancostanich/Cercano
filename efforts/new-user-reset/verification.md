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
