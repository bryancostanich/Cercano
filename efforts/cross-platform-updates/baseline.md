# Implementation baseline

Base: origin/main `2668baa1`; isolated branch `feat/cross-platform-updates` in
`Cercano-cross-platform-updates`. Approved spec and plan copied byte-for-byte
before semantic task updates. Original checkout and developer installation were
not modified. No production code changes, remote writes, secret reads or signing.

## Existing seams and real gaps

- `source/server/pkg/update/update.go`: GitHub discovery, 24-hour cache, three-second
  HTTP timeout. Calls in `source/server/cmd/cercano/main.go:1009,1897` run inline
  despite comments describing them as nonblocking. Installation detection runs
  `brew list cercano` if brew exists; it does not tie that installation to the
  executing binary. A developer checkout can be misclassified when brew also
  has Cercano. No new classification is implemented yet.
- `source/server/internal/server/` has update-info reporting and standing
  `SubscribeEvents` (`events.go`); `source/clients/cli/internal/ui/event_subscription.go`
  already consumes the standing stream and `model.go` owns Bubble Tea updates.
  Extend existing transport conventions rather than assume no notification path.
- `source/proto/agent.proto:37,206` exposes `ShutdownAgent` and `SubscribeEvents`.
  Version detection for an update must remain separate from connection gating.
- `source/server/internal/server/shutdown.go:75`: existing drain is a bounded
  shutdown with a force-stop backstop, NOT the approved wait-for-idle policy.
  Reuse must insert an update admission/idle boundary before destructive shutdown;
  merely calling ShutdownAgent is insufficient to promise no cancelled work.
- `source/server/internal/brewrestart/coordinator.go`: ownership, preflight,
  shared launch lock, identity recheck, shutdown, confirmed exit, start and
  readiness are already separate testable operations. Native adapter is Darwin
  only; other platforms explicitly reject this restart path.
- `source/server/pkg/agentclient/client.go`: auto-launch prefers a sibling binary
  then PATH; reconnect can relaunch. Stable launcher/package selection must
  coordinate with this path rather than only the primary UI process.
- `source/server/pkg/agentclient/launch_lock_unix.go`: existing OS launch lock.
  `launch_lock_other.go` is no-op on Windows; `detach_other.go` returns nil.
- `source/server/internal/procx/procx_other.go`: only direct child is killed;
  no equivalent descendant-group cleanup. Native Windows lifecycle remains work.
- `release/homebrew/cercano.rb.in`: both binaries in one keg; post-install invokes
  restart-after-upgrade. An app-held lock can deadlock with that hook unless the
  backend defines its lock boundary. Formula smoke tests deliberately avoid a
  real restart.
- `release/` currently contains only Homebrew packaging. Windows/Linux builders
  produce archives, not Chocolatey/deb installations. APT scope markers and
  a signed repository cannot be inferred from those archives. Direct archive
  enrollment is explicitly separate from existing executable discovery.
- Existing authentication/config/user data paths remain outside the installation.
  They must not be used as authority to select another user's install/process.

## Spike fixture audit

Only `cmd/fixture-agent` and `cmd/fixture-cli` from the Windows spike were copied
into the independent `fixtures/` module here. Both use the standard library.
No prototype updater, library dependency, manifest logic or branch-only workflow
was promoted. Corrected the fixture comment claiming parent death terminates it:
parents must explicitly own, stop and reap subprocesses. Holding an executable
open remains a limited file-lock probe, not universal process-ownership proof.

Both extracted fixtures compile for Windows x64, Linux x64 and macOS arm64.
macOS fixture `--version` ran successfully. Other target binaries were not run.
All temporary compiled files were removed automatically.

## Tests actually run

- Server: `go test ./pkg/update ./internal/brewrestart ./pkg/agentclient ./internal/procx ./cmd/cercano` — all five packages passed.
- Server drain/events: `go test ./internal/server -run 'TestDrain|TestBeginShutdown|TestSubscribe|TestIdleShutdown' -count=1` — passed.
- CLI: `go test ./internal/ui -run 'Test.*(Event|Reconnect|Update|Version|Shutdown)' -count=1` — passed.
- Homebrew Python: 57 tests passed. Existing socket ResourceWarnings were emitted;
  do not claim a warning-free baseline. No fixture cleanup patch made here.
- Workflow Python: 40 tests passed.
- macOS builder: 11 tests passed.
- Windows builder: 11 tests passed.
- Linux builder: 14 tests passed.

These are current macOS baseline results, not new native Windows/Linux acceptance.
Previously recorded CI spike results remain historical evidence only.

## Resources and authorization boundaries

- Current host is macOS arm64; there is no established local Linux/Windows VM for
  this effort. Fresh remote native CI runs require authorized branch/workflow
  publication, which the run brief does not presently grant.
- Local model delegation failed to start due to the memory guard. No model routing
  or runtime processes were changed; bounded direct tools were used for baseline.
- TUF production keys, metadata host, online signing permission and rotation/expiry
  policy are unprovisioned. The next phase uses ephemeral test keys/local HTTP only.
- No Chocolatey installer/signing identity or signed APT repository is established
  by this baseline. Privileged installer/manager rehearsals require disposable
  machines and explicit authorization, not this developer's installation.
- Windows Authenticode provisioning, helper bootstrap trust, TUF key custody and
  metadata hosting remain the explicit decision gates from the approved plan.
- Existing release credentials are outside this audit. None were inspected or
  assumed available to test tooling. No builds were signed or submitted to Apple.
