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

## Not implemented yet

This is a safety component, not an operational restart command. Remaining work includes socket-to-process attribution, preserving launch settings, coordinating with client launches, shutdown/drain and actual-process-exit handling, replacement startup/readiness checks, command and formula hook wiring, and isolated end-to-end tests. There is no claim that direct brew upgrades now restart the agent. No tap/release artifacts were published.
