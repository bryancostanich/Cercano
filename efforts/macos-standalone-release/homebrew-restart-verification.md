# Homebrew upgrade restart — implementation progress

## Superseding requirement

The user explicitly replaced the previous no-automatic-restart rule: successful direct `brew upgrade cercano` must restart an already-running agent. An absent agent stays absent; a failed installation must not stop the existing agent. No version handshake or compatibility gate is required. The approved approach is a post-install restart coordinator with macOS process inspection for ownership.

The earlier Phase 2 completion and running-agent-reuse checks do not establish this new requirement. The existing restart task has been reopened; its original title still describes the superseded contract.

## Implemented: kernel process identity and installation ownership

`source/server/internal/brewrestart` reads PID, effective user ID, executable path and process start time through macOS libproc. Identity is sampled before and after the path query to detect disappearance/PID reuse during inspection. Comparisons include the start timestamp so a reused PID is not treated as the same process.

Ownership accepts only a canonical `Cellar/cercano/<version>/bin/cercano` path under the same formula root as the new executable and the current user. It rejects development paths, another prefix/user/formula, prefix symlinks, missing identity and noncanonical paths. Unsupported hosts/builds fail closed.

The kernel executable path is deliberately not resolved through a Homebrew prefix symlink: that link can now refer to the NEW binary while the OLD binary still runs. Command text is not proof of executable identity.

## Verification performed

- `go test ./internal/brewrestart -count=1 -v`: passed.
- `go vet ./internal/brewrestart`: passed.
- `CGO_ENABLED=0 go test ./internal/brewrestart -count=1`: passed (ownership logic builds independently; native inspection disabled).
- Integration test launches a copied Go test executable in a temporary fake Cellar, spoofs argv[0] as the new binary, repoints the invocation symlink, and verifies kernel identity still points to the old binary. It then terminates/reaps only that child and verifies the exited process is no longer inspectable.
- An initial copied `/bin/sleep` fixture was killed by macOS (separate probe observed exit signal 9). Replaced with the copied Go test executable; no production workaround was added.

No live Cercano process was connected to, signaled or restarted. No Homebrew operation was invoked.

## Implemented: launch-state capture and per-process listener verification

`CaptureLaunchState` reads kernel `KERN_PROCARGS2` and `PROC_PIDVNODEPATHINFO` records for a known same-user process. It preserves argument boundaries (including empty arguments), environment entries, and the working directory. Identity is revalidated before and after capture. Malformed kernel records and changed identities fail closed. LaunchState formatting reports counts only, not arguments, environment values or working-directory contents; captured state is not persisted.

`HoldsListener` uses `PROC_PIDLISTFDS` and `proc_pidfdinfo(PROC_PIDFDSOCKETINFO)` to verify an exact loopback TCP listener on a known process. It checks TCP LISTEN state and address/port and binds inspection to the expected process identity. Wildcard/remote endpoints, unstable descriptor snapshots and changed identities are rejected. It does not enumerate the machine's processes or prove unique socket ownership.

A real isolated child test opens an ephemeral listener, exposes its endpoint, and waits for an explicit test command to close it. The parent verifies capture of its supplied argv/environment/cwd, listener presence and subsequent absence, stale-identity rejection, and rejection after process exit. Only the test child is inspected and terminated. Parser tests cover padding, empty arguments, environment values containing equals signs, malformed records and secret-redacting formatting.

Additional verification passed:

- `go test ./internal/brewrestart -count=10`
- `go test -race ./internal/brewrestart -count=1`
- `go vet ./internal/brewrestart`
- `CGO_ENABLED=0 go test ./internal/brewrestart -count=1`

## Not implemented yet

These are safety components, not an operational restart command. Remaining work includes candidate-process discovery and unique socket ownership, coordinating with client launches, shutdown/drain and actual-process-exit handling, replacement startup/readiness checks, command and formula hook wiring, and isolated end-to-end tests. Capturing launch state alone does not prove a restarted agent preserves its settings. There is no claim that direct brew upgrades now restart the agent. No tap/release artifacts were published.
