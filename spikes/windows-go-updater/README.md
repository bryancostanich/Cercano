# Spike: Windows Go updater (`go-selfupdate` vs Velopack)

Isolated feasibility spike for updating the **two** Cercano Windows binaries
(`cercano.exe`, `cercano-cli.exe`) from the single published
`cercano-<version>-windows-x64.zip` release artifact.

- **Status:** throwaway prototype. This directory is an **independent Go
  module** (`github.com/bryancostanich/Cercano/spikes/windows-go-updater`) —
  no production code or dependencies were touched.
- **Verdict up front:** `github.com/creativeprojects/go-selfupdate`
  **v1.6.0** is a viable *library substrate* (detection, download, explicit
  hash validation, single-binary extraction), but its stock updater **cannot
  update two binaries together** — a thin external coordinator is required.
  Velopack was **ruled out for Go**: no Go SDK exists
  (`github.com/velopack/velopack-go` → 404; the repo has no tagged Go module),
  so the spike proceeded with go-selfupdate.
- **Not a product decision.** The versioned-directory coordinator here is a
  prototype demonstrating feasibility and honest failure modes only.

## Library under test

| Dependency | Version | License |
| --- | --- | --- |
| `github.com/creativeprojects/go-selfupdate` | **v1.6.0** (pinned in `go.mod`; latest on proxy at spike time) | MIT |
| `github.com/gofrs/flock` (cross-process lock) | v0.12.1 | MIT-style (per module LICENSE) |

## Facts (verified locally with real APIs and real OS behavior)

Every fact below is pinned by a named test. Nothing was simulated that could
be observed; where observation was impossible locally (Windows file-lock
semantics), it is explicitly deferred (see *Remaining Windows-native
verification*).

### Release detection against the exact Cercano naming

Cercano releases (per `scripts/build-windows-release.py`): tag `vX.Y.Z`,
archive `cercano-<version>-windows-x64.zip` containing a single nested root
`cercano-<version>-windows-x64/bin/{cercano.exe,cercano-cli.exe}` plus
`LICENSE`/`README.txt`, and a `.sha256` sidecar (`<hex>  <basename>\n`).

- `DetectLatest` with `Config{OS: "windows", Arch: "x64"}` matches the exact
  asset name — evidence: `lib/detect_test.go:TestDetectLatestMatchesExactCercanoNamingWithArchX64`.
- Stock arch suffixes for **amd64** (`windows_amd64`, `windows-amd64`, …) do
  **not** match `windows-x64`; an amd64-configured updater finds nothing —
  evidence: `TestDetectLatestAmd64SuffixesMissCercanoX64Asset`. (Must use
  `Arch: "x64"` or `Config.Filters`, e.g. `windows-x64\.zip$` —
  `TestFiltersCanSelectCercanoX64Asset`.)
- Drafts/prereleases are skipped unless `Config.Draft`/`Config.Prerelease` —
  `TestDetectLatestSkipsDraftAndPrereleaseByDefault`.
- Tests use a local `FakeSource` implementing the library's real `Source`
  interface (`lib/fakesource.go`); no network, no credentials, and **no
  remote bytes were ever executed** — all fixtures are built from this
  spike's own source.

### Integrity validation is OPT-IN, not default

- Stock `SHAValidator` accepts the exact sidecar format Cercano already
  publishes — `lib/validate_test.go:TestSHAValidatorAcceptsExactCercanoSidecarFormat`.
- With **no** validator configured, a tampered archive's bytes are installed
  unvalidated — `TestNoValidationByDefaultAppliesTamperedBytes` (documents
  the default is *no* validation; the coordinator sets `SHAValidator{}`
  explicitly).
- A tampered archive (correct shape, wrong bytes) is rejected by
  `ErrChecksumValidationFailed` before anything is applied —
  `TestSHAValidatorRejectsTamperedArchiveBeforeApply`.
- `ChecksumValidator`, `ECDSAValidator`, `PGPValidator`, and
  `PatternValidator` also exist in v1.6.0 (API inspected in
  `…/go-selfupdate@v1.6.0/validate.go`); only SHA-256 was exercised.

### Stock updater: two binaries = two independent, NON-transactional operations

This is the spike's central library finding — **do not claim multi-exe
transactional ability from the stock API**:

- `UpdateCommand` replaces only the targeted executable path; the other
  binary keeps running the old version —
  `lib/stockupdate_test.go:TestStockUpdateCommandReplacesOnlyTheTargetedBinary`.
- Updating both binaries is two independent operations; injecting a download
  failure into the second leaves a **mixed install** (agent new, cli old)
  with **no rollback** —
  `TestStockTwoBinaryUpdateIsTwoIndependentNonAtomicOperations`. It also
  proves the archive is re-downloaded per binary (2 downloads).
- The archive is buffered whole in memory **per extraction call** and the
  stock path has **no size limit** (DoS constraint for production: wrap the
  source with a limiting reader) —
  `lib/extract_test.go:TestStockZipPathBuffersWholeArchivePerCallAndHasNoSizeLimit`.

### Archive extraction semantics (nested root)

- `DecompressCommand` finds each binary inside the nested
  `cercano-<v>-windows-x64/bin/` layout by **base filename** and returns one
  binary per call; there is no stock multi-file extraction —
  `lib/extract_test.go:TestDecompressExtractsEachExeFromNestedRootLayout`.
- Missing command ⇒ `ErrExecutableNotFoundInArchive` —
  `TestDecompressFailsWhenCommandMissingFromArchive`.

### Hostile archive shapes (observed stock behavior, `lib/safety_test.go`)

- **Duplicates:** the *first* member wins (benign-first fixture extracted the
  benign bytes). No duplicate rejection — production should reject archives
  with duplicate member names.
- **Traversal:** base-name matching also matches traversal-named members
  (`../../cercano.exe`). The library itself returns bytes (never writes
  member-named paths), and this spike's coordinator writes **only fixed
  paths** (`versions/<v>/bin/<name>`), which neutralizes traversal — but
  naive extraction code would be vulnerable.
- **Symlinks:** a symlink member claiming to be `cercano-cli.exe` is **not
  refused**; its content (the link-target path string) is returned as the
  "binary". Extracted payloads must be sanity-checked before use.
- **Primary control:** explicit SHA-256 validation rejects any archive whose
  bytes don't match the published sidecar *before* staging — shape-hardening
  is defense-in-depth, not the main gate.
- Missing binaries and sandboxing: staging rejects archives missing a
  required binary (`updater/updater_test.go:TestArchiveMissingOneBinaryIsRejectedBeforeActivation`
  asserts `ErrExecutableNotFoundInArchive`); all fixture/release files live
  only under `t.TempDir()` sandboxes.

### POSIX file-lock facts (observed with real processes on macOS)

- A **running** binary can be renamed while its process keeps running —
  `updater/lockbehavior_test.go:TestPOSIXRunningBinaryCanBeRenamed`.
- An open binary can even be deleted; the running process is unaffected —
  `TestPOSIXOpenFileDeleteSucceeds`.
- Therefore versioned-directory activation is safe on POSIX even while the
  old agent runs — end-to-end proven by
  `TestPOSIXUpdateWhileOldAgentRunsSucceeds` (old process untouched, old
  version dir immutable, new version live after one manifest commit).

## Proposed architecture demonstrated (NOT a product decision)

`updater/` is a throwaway coordinator proving a *commit-point* design:

```
install/
  versions/v0.21.0/bin/{cercano.exe, cercano-cli.exe}   (immutable per version)
  versions/staged-v0.21.0{,.tmp}/                        (staging, try-then-rename)
  active.json        (active-version manifest; ONE atomic write = commit point)
  .update.lock       (OS lock via gofrs/flock; single updater process)
```

Sequence: **Stage → Activate**, under `RunWithLock`:

- `Stage`: detect latest (real `DetectLatest`), download once via the real
  `Source`, apply the *explicit* `SHAValidator`, extract **both** binaries
  from the single buffered archive into `staged-vX.tmp`, rename to
  `staged-vX`, refuse if an active binary is running (Windows probe).
- `Activate`: rename `staged-vX → versions/vX`, then atomically replace
  `active.json` (temp file + rename). The manifest write is the **commit
  point**; if it fails, the directory rename is rolled back.
- `RestorePrevious(version)`: watchdog entry point — flips the manifest back
  to a retained previous version dir.

Demonstrated in `updater/updater_test.go` (all fixture binaries, no live
Cercano, no remote execution):

- Both binaries flip together in one commit —
  `TestCoordinatorStagesAndActivatesBothBinariesTogether`.
- Injected download failure before activation ⇒ old manifest + old binaries
  untouched, no staging leftovers —
  `TestInjectedFailureBeforeActivationKeepsOldManifest`.
- Archive missing one binary rejected during staging with
  `ErrExecutableNotFoundInArchive` —
  `TestArchiveMissingOneBinaryIsRejectedBeforeActivation`.
- Tampered archive rejected by explicit SHA validation before staging —
  `TestTamperedArchiveFailsValidationAndActivatesNothing`.
- Health failure after activation ⇒ supervisor calls `RestorePrevious`, old
  manifest restored, both binaries resolve to the old version —
  `TestHealthFailureAfterActivationRestoresOldManifest`.
- Cross-process lock: a real subprocess holding the lock excludes a second
  updater; the lock file is removed on exit —
  `TestCoordinatorLockPreventsConcurrentUpdaters`.

### What is explicitly NOT claimed

- **No transaction runtime:** the coordinator does not pause the agent, does
  not supervise processes, and does not automatically watch health. "Restart/
  health failure restores old manifest" is demonstrated via the explicit
  `RestorePrevious` entry point only.
- **No signing keys, secrets, GitHub credentials, or app-lifecycle work.**
  Validation here is SHA-256 only; signatures (ECDSA/PGP) were inspected but
  not exercised.
- Idempotency: updating to the already-active version is a no-op returning
  `Activated: false`.
- The staged/activated binaries are **never executed during staging** —
  only *after* activation does the supervisor run the health check.

## Remaining Windows-native verification

The host was macOS; POSIX facts above are proven only for macOS/Linux
semantics. The following are encoded as Windows-only tests
(`updater/lockbehavior_windows_test.go`, build tag `windows`) that
cross-compile (verified: `GOOS=windows go vet ./...` and `go test -c` both
pass) but were **not executed locally**:

1. A running `.exe` cannot be renamed (sharing violation) —
   `TestWindowsRunningBinaryRenameFails`.
2. `Stage` refuses to activate while an active binary runs (probe =
   no-op rename in `running_windows.go`) —
   `TestWindowsActivationBlockedWhileAgentRuns`.
3. Running `go test ./updater/` on real Windows.
4. End-to-end Windows fixture runs (fixture exes cross-compile to PE32+).

The parent task will add a credential-free, branch-specific Windows CI
probe workflow to run these; no workflow is included in this spike.

## Licensing

- `go-selfupdate` v1.6.0: MIT (LICENSE in module cache).
- `gofrs/flock` v0.12.1: MIT-style per its LICENSE.
- Velopack (not adopted, no Go SDK) is MIT-licensed upstream; no Go module
  is consumable today, so it was excluded before licensing mattered.

## Running the experiments

```sh
cd spikes/windows-go-updater
go test ./... -race -count=1      # host (macOS/Linux) facts + coordinator
GOOS=windows GOARCH=amd64 go vet ./...   # Windows-only tests compile
GOOS=windows GOARCH=amd64 go test -c -o /tmp/win-updater.test ./updater
```

Module: `github.com/bryancostanich/Cercano/spikes/windows-go-updater`
(Go 1.25.12, independent `go.mod`; production repo untouched).
