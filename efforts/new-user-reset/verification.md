# Setup reset verification — in progress

Implementation worktree: Cercano-new-user-reset, branch feat/new-user-setup-reset, base d9d21fd3. No real reset, user-config mutation, credential reads/deletes, service shutdown, inference or model-file deletion performed.

## Baseline and pure configuration slice

Baseline `go test ./pkg/config ./internal/secrets ./internal/hostsvc/credentials ./internal/chatgptauth ./internal/anthropicauth -count=1`: all five packages passed (temporary/in-memory fixtures). Log: /tmp/cercano-reset-baseline-server.log.

Added ResetSetupYAML with explicit root/nested ownership. Unknown unrelated YAML keys and feature preferences survive; complete setup-owned subtrees restore current Defaults; omitted legacy defaults are removed. Conservatively rejects aliases/merges, duplicate/non-string keys, multiple documents, malformed known types and non-mapping roots. Formatting can normalize. It has no filesystem/keychain dependencies.

New API tests initially failed compilation before implementation. Complete pkg/config suite then passed. Additional parent audit reproduced yaml.TypeError echoing `private...` from an invalid config field, which could expose credential fragments. Type-validation errors are now sanitized rather than including the parser's input snippets. Regression and complete pkg/config suite passed after the fix (latest run 1.644s).

Inventory records remaining coordination and wizard boundary work. Command, orchestration, credential clearing and process coordination are not yet implemented or verified. No whole-feature completion claim.
