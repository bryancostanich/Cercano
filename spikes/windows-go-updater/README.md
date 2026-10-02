# Go-native Windows updater spike (not production)

This isolated module investigates `creativeprojects/go-selfupdate` **v1.6.0**
(MIT), using local fixture releases and locally compiled test executables. It
changes no production updater, installation, credentials, or release artifacts.
`gofrs/flock` v0.12.1 supplies the prototype's OS-level coordinator lock.

## Findings

- Release discovery works with Cercano's existing nested ZIP and `windows-x64`
  asset naming when configured explicitly. The stock `amd64` selector misses
  that suffix; `Arch: "x64"` or a matching filter is necessary.
- SHA-256 validation is opt-in. With no validator, the library can install
  modified bytes. Explicit `SHAValidator` accepts our sidecar format and rejects
  mismatches. This verifies integrity against a sidecar, **not independent
  publisher authenticity**; a production updater needs a signing/trust design.
- Stock `UpdateTo` updates **one executable**. Updating agent then client can
  leave mixed versions if the second operation fails. Library rollback does
  not make the pair a transaction.
- The stock path downloads/buffers the archive per update call. Its extractor
  matches basenames and accepts ambiguous duplicate, traversal-named, or symlink
  entries in our tests. These tests do not demonstrate arbitrary-path writes:
  the caller chooses the destination. They do show it is not a strict validator
  of the expected two-binary package.
- There is no installer, Chocolatey synchronization, or Cercano process drain
  coordinator supplied by this library.

## Prototype experiment

`updater/` uses real library detection, validation and extraction APIs, with a
small **experimental**, separate coordinator:

1. Take a persistent OS lock for this sandbox installation.
2. Require an explicit validator and download one ZIP, bounded to 256 MiB
   compressed, with a 64 KiB validation-sidecar limit.
3. Reject duplicate, unexpected, nonregular, missing or unsafe archive members
   before extraction. Enforce a 512 MiB declared expanded limit and fixed paths.
4. Stage the two fixture executables in an immutable version directory.
5. Switch a single `active.json` manifest used by the test launcher.
6. On an injected post-activation health failure, explicitly restore the old
   manifest, then run both old fixtures to confirm recovery.

This tests a **single selection point** for both binaries, not a complete
production transaction runtime. In particular, it does NOT establish Windows
power-loss durability or guarantee atomic file replacement under every
filesystem/security-software configuration. Production launchers would need to
resolve one version consistently and coordinate with updates.

The rollback test calls `RestorePrevious` explicitly after fixture health
failure. There is no automatic watchdog or long-running process supervisor.
Before-activation failure leaves the original manifest unchanged.

## Native Windows evidence

First native run:
<https://github.com/bryancostanich/Cercano/actions/runs/37065950392>

The library tests, staging/activation/rollback tests, and held-open executable
probes passed. The run failed because the prototype removed its lockfile before
unlocking it, and the test required removal. Windows retained the locked file.
Deleting the lock pathname also risks a Unix lock-identity race, so the fix
retains the pathname and releases only the OS lock. The test now verifies both
contention and successful reacquisition after release.

**Scope of the file-lock probe:** it runs a fixture that explicitly opens and
holds its own executable. That fixture's rename fails on the Windows runner.
This does NOT prove that every running Windows executable is unrenameable or
that a rename probe reliably identifies live Cercano processes. The prototype
check remains best-effort, with a time-of-check/time-of-use race. A production
update must coordinate launch/drain explicitly rather than rely on this probe.

A subsequent native run verifies the corrected lock lifecycle and strict staging
gate; its result is recorded below once observed.

## Running the spike

```sh
cd spikes/windows-go-updater
go test -v -count=1 ./...
go test -race ./...  # supported native race toolchain required
```

All application executables run by tests are built from `cmd/fixture-*` in this
module. Fake release sources serve fixture bytes; no remote application binary
is executed. Tests use temporary directories. Helper-only subprocess tests are
skipped in the parent invocation and exercised via their parent tests.

`.github/workflows/windows-update-spike.yml` runs only on pushes to
`spike/windows-go-updater`, with a read-only token and no release environment.
It is diagnostic scaffolding, not intended for landing wholesale on main.

## Recommendation and boundaries

The Go library is useful for **release discovery and explicit verification**.
It is not a drop-in Velopack replacement for two-binary installation, recovery,
process supervision, or installer integration. Lack of a documented Go SDK does
not rule out Velopack's native API/helper integration; that remains an option.

A production Go-native solution still requires decisions and tests for:

- Per-user versus managed/machine-wide ownership and Chocolatey coexistence.
- Stable launchers/PATH, and whole-package version selection.
- Windows process identity, a shared launch/update lock, drain deadlines, and
  explicitly consented handling of work that cannot drain.
- Signed update metadata/assets, trust-key rotation, and downgrade policy.
- Robust cancellation, downloads, permissions, ACLs, hostile archives, and
  cleanup of abandoned stages (this prototype is not a security boundary).
- Interrupted activation/recovery and real restart health checks.
- Antivirus, long/Unicode paths, uninstall and data preservation.

This spike does **not** approve the versioned-directory design for production.
No changes have been made to Cercano's installer, Chocolatey packaging, user
configuration, signing setup, published versions, or main branch.
