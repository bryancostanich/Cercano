# Cercano macOS Standalone Release — Consolidated Phase 1 Audit

Date: 2026-09-22 · Branch: `release/macos-standalone` @ `bbe36575` (clean worktree, no upstream, ahead 0 / behind 0)

This report consolidates the Phase 1 audit work recorded in `startup-audit.md`, `build-inputs.md`, `signing-audit.md`, `update-audit.md`, `offline-version-verification.md`, `deployment-target-probe.md` / `-followup.md`, `unsigned-rehearsal.md`, and the earlier `discovery-test-extract.md` probe (which was superseded — see §1). No code or behavior changes were made in this audit.

---

## 1. Entrypoints and binary discovery — VERIFIED

### Entrypoint trace
- `source/clients/cli/main.go:1-6` — `cercano-cli` is a thin gRPC client; connects to a running agent at `localhost:<cfg.Port>` or auto-launches `cercano agent` on miss.
- `source/server/pkg/agentclient/client.go:88-120` (`Dial`) — 600 ms quick connect, then `ensureServerLaunched` under a cross-process auto-launch lock (`launch_lock_unix.go`), then connect with 3 s timeout; re-checks before spawning so concurrent clients don't double-spawn.
- `source/server/pkg/agentclient/client.go:172-198` (`autoLaunchServer`) — spawns `<binary> agent` detached (setsid), logs to `$TMPDIR/cercano-server.log`, marks `CERCANO_AUTOLAUNCHED=1` so the server self-shuts-down when the last client leaves; singleton behavior preserved for manually started servers.

### Binary discovery (Homebrew symlink safe)
- `source/server/pkg/agentclient/client.go:200-213` (`findCercanoBinary`): (1) sibling of `os.Executable()` — which resolves through Homebrew's `bin/` → `Cellar/...` symlinks on darwin, so the server is always discovered from the same keg as the CLI; (2) `exec.LookPath("cercano")` fallback; explicit error otherwise.
- Test evidence (resolves the null-result probe in `discovery-test-extract.md`, which used non-matching search patterns): `source/server/pkg/agentclient/binary_discovery_test.go:21` `TestBinaryDiscovery` runs a real subprocess and covers **homebrew_symlinks** (prefix `bin/` symlinks → Cellar resolution), **path_fallback**, and **missing** modes, asserting same-file identity (`os.SameFile`).
- Co-location requirement is enforced by installation layout: both binaries in one keg/archive root (`scripts/release-macos-archives.sh`, per `build-inputs.md`).

### Version plumbing
- `source/clients/cli/Makefile:5` and `source/server/Makefile:4` — `VERSION ?= $(git describe --tags --always --dirty)`, injected via `-X main.version=...`.
- `source/clients/cli/release_version_test.go:20` `TestReleaseVersionOffline` — `--version` prints `cercano-cli v<version>` and performs **zero** HTTP requests (probe transport). Confirms `offline-version-verification.md`.

Exact test commands:
```
cd source/server && go test ./pkg/agentclient -run TestBinaryDiscovery -count=1
cd source/clients/cli && go test -run TestReleaseVersionOffline .
```

**Status: PASS.** Discovery is symlink-safe and test-covered; upgrade path must keep both binaries in the same directory (same keg), which a Homebrew bottle upgrade does atomically.

---

## 2. Assets, runtime downloads, and licenses — VERIFIED, one gap

- **Bundled in archive**: `cercano`, `cercano-cli`, `LICENSE` (Apache-2.0) only (`scripts/release-macos-archives.sh`; `build-inputs.md`). No filesystem-relative assets at runtime — embedded assets are limited to generated protobuf code; configuration lives under `~/.config/cercano` and models under `~/.local/share/cercano/models` (`startup-audit.md`, `source/clients/cli/internal/uiconfig/uiconfig.go:18-21`).
- **Optional runtime integration**: llama-server binary. Detected via PATH/filesystem checks; suggested install `brew install llama.cpp` (`source/server/internal/localruntime/llamaserver/detect.go:58-66`, `SuggestedCommand`). Non-interactive detection returns typed `DetectError` for missing binary/model.
- **Runtime downloads**: model GGUFs fetched on explicit user action as OCI blobs from `registry.ollama.ai` into `~/.local/share/cercano/models` (server llamacatalog; per `startup-audit.md` and earlier grep hits on `ociManifestURL`/`modelBlobsPath`). Nothing is downloaded at startup or by `--version` (verified by `TestReleaseVersionOffline`).
- **License/redistribution**: archive redistributes only Apache-2.0 project code plus statically linked Go modules. **Gap**: the archive carries only the Apache-2.0 `LICENSE`; no third-party module license/notice file yet. Recommend adding a `NOTICE`/`THIRD-PARTY` summary (e.g., from `go mod` license data) in the packaging step before public release.

---

## 3. Workflows, updater behavior, and tap conventions — VERIFIED, two findings

### Updater behavior (no changes needed)
- `source/server/pkg/update/update.go:18` — checks `https://api.github.com/repos/bryancostanich/Cercano/releases/latest` (matches the real, existing repo). 24 h cache, 3 s timeout, silent on any failure (never blocks the user). Prereleases skipped (`update.go:84-86`). Semver comparison (`CompareVersions`); upgrade command is `brew upgrade cercano` when `DetectInstallMethod()` == "homebrew" (checks `exec.LookPath("brew")` + `brew list cercano`, `update.go:185-193`), otherwise a manual download URL.
- Test evidence: `source/server/pkg/update/update_test.go` — 13 tests including `TestCheckForUpdate_SkipsPrerelease`, `TestUpgradeCommand_Homebrew`, `TestCheckCached_*` cache-fallback matrix.
```
cd source/server && go test ./pkg/update
```
- Upgrade UX satisfies the approved decision: non-disruptive (advisory prompt only), never kills a running agent, no background service installed.

### Release workflow / releases (public, read-only)
- `bryancostanich/Cercano` already has public releases through **v0.10.0 (latest)**, each published by the `github-actions` bot with 5 assets.

#### CORRECTION (verified 2026-09-22 by direct inspection of `.github/workflows/release.yml`)
An earlier draft of this section claimed the existing release workflow was "live and functioning" with signing. **That claim was wrong.** Direct inspection shows `.github/workflows/release.yml` (tag-triggered, `permissions: contents: write`):
- **No signing, no notarization, no keychain, no cosign.** `grep -rn 'codesign\|notariz\|cosign\|keychain' .github/workflows/` returns zero matches.
- **Builds only the server binary** (`./cmd/cercano`); `cercano-cli` is never built or published. The spec's two-binary distribution does not exist in the workflow.
- **Builds raw binaries, not archives** — no `.tar.gz`/`.zip`, no `LICENSE`, no SHA-256 checksums.
- **Wrong platform matrix for this effort**: darwin/arm64, darwin/amd64, linux/amd64 — the approved spec is Apple Silicon only.
- **Builds on `ubuntu-latest`**, which cannot codesign or notarize macOS binaries at all.
- Release notes are auto-generated and every artifact is uploaded unconditionally; nothing gates on verification.

Consequence: Phases 3 and 4 are **greenfield**, not adjustments to a working pipeline. The prior `signing-audit.md` / `unsigned-rehearsal.md` notes describe *local, unsigned* rehearsal tooling (`scripts/build-macos-unsigned.sh`, `scripts/test-macos-unsigned.py`), not CI signing.

#### CORRECTION: packaging script does not exist
This report repeatedly cited `scripts/release-macos-archives.sh` as the packaging step. **That file does not exist.** `ls scripts/` shows exactly two files: `build-macos-unsigned.sh` and `test-macos-unsigned.py`. All statements in this document sourced to `release-macos-archives.sh` (archive contents, `LICENSE` inclusion, co-location layout) are therefore **not verified by that path**.

Re-read of `build-macos-unsigned.sh` (done 2026-09-22) **substantively confirms the §2 archive claims**: it refuses non-Darwin/non-arm64 hosts, validates an `X.Y.Z[-suffix]` version, builds **both** `bin/cercano` and `bin/cercano-cli` with `CGO_ENABLED=1 -trimpath` and `-X main.version`, copies `LICENSE`, emits a `tar.gz` plus a `.sha256`, refuses to overwrite an existing archive, and cleans its temp dir via trap. Differences from the old claim: the staged layout puts binaries under `bin/` (not the archive root), and the archive also carries a `README.txt` marking it an unsigned rehearsal build. It performs **no signing** — as its name states.

### Tap conventions (public, read-only via fetch)
- `bryancostanich/homebrew-tap` is public; README advertises `brew tap bryancostanich/tap`; currently ships one **cask** (`lattice`) and **no `Formula/` directory** — `Formula/cercano.rb` returns 404 on both `main` and `master` (raw URLs 404).
- **Finding (blocker)**: existing release notes already tell users `brew upgrade cercano`, and the updater recommends it when brew detection succeeds — but **no cercano formula is published in the tap**. A binary formula `Formula/cercano.rb` (class `Cercano < Formula`, arm64 macOS) must land in the tap before the first standalone release. No remote changes were made in this audit.

---

## 4. Compile floor vs runtime verification

- **Compile floor**: `MACOSX_DEPLOYMENT_TARGET=12.0` / `macosx12` SDK floor established by probe (`deployment-target-probe.md`, follow-up). ARM64 Go builds require ≥ macOS 12, so **declared supported floor: macOS 12 (Monterey), Apple Silicon only**.
- **Runtime verification**: NOT performed on a macOS 12 machine. Clean-machine smoke test (fresh VM: `brew tap` + `brew install cercano`, `cercano-cli --version`, auto-launch, one model chat) is the **remaining verification blocker**. This audit makes no claim that macOS 12 was tested.

---

## 5. Compatibility contract — RESOLVED, no code changes

- Current behavior: `Dial`/`connect` (`source/server/pkg/agentclient/client.go:88-137`) is a plain gRPC connection with **no version handshake and no version gating anywhere**. Mixed-version client/server connections already work by design; the agent server is a shared singleton serving multiple clients.
- User clarification (recorded 2026-09-22, appended to `plan.md` as addendum): **preserve mixed-version connections; version mismatch alone is never a blocker; no new TUI/agent handshake or version gating.** The existing code already satisfies this — no protocol work is needed and none was made. The investigation item is closed on the user's resolution.

---

## Verification status of this report (2026-09-22, parent agent)

Re-verified directly, not taken on the sub-agent's word:
- **PASS** — all three cited tests actually run green: `go test ./pkg/agentclient -run TestBinaryDiscovery -count=1`, `go test ./pkg/update`, `go test -run TestReleaseVersionOffline .` (all `ok`).
- **PASS** — `findCercanoBinary` sibling-then-PATH logic is as described (`client.go:200-213`); `TestBinaryDiscovery` really does cover `homebrew_symlinks` / `path_fallback` / `missing`.
- **PASS** — updater endpoint, prerelease skip, and `brew upgrade cercano` string confirmed in `update.go`; `brew install llama.cpp` confirmed in `detect.go:61`; both Makefiles inject `main.version` from `git describe`.
- **CORRECTED** — release workflow signing claim (see §3 correction). The workflow has no signing and omits `cercano-cli`.
- **CORRECTED** — `scripts/release-macos-archives.sh` does not exist (see §3 correction).

## Remaining blockers (actual, as of this audit)

1. **macOS 12 clean-machine runtime smoke test** — floor is compile-verified only; must be exercised on a real/virtual Monterey Apple Silicon machine before claiming support. Do not claim it tested until done.
2. **Tap formula missing** — `Formula/cercano.rb` does not exist in `bryancostanich/homebrew-tap` while release notes and the updater already reference `brew upgrade cercano`. Highest-priority Phase 2 implementation item.
3. **Third-party license notices** — archive ships only the Apache-2.0 `LICENSE`; add a third-party notices file (or repo link) before public distribution. Decision needed in packaging phase.
4. **No macOS release pipeline exists** — `.github/workflows/release.yml` builds an unsigned, server-only, un-archived binary set on Linux runners. Every spec requirement for Phase 3/4 (macOS runner, two binaries, archives + checksums, Developer ID signing with hardened runtime, notarization, fail-closed gating, Apple Silicon-only matrix) is unimplemented. This is the largest remaining work item and was previously mis-reported as already working.

Non-blockers / notes:
- End-to-end installed-layout coverage: the Homebrew symlink layout is covered by unit fixtures (`TestBinaryDiscovery`), but no live `brew install` integration test exists; recommend as part of blocker 1's smoke test rather than separate work.

## Test command index

```
# Binary discovery through Homebrew symlinks / PATH / missing
cd source/server && go test ./pkg/agentclient -run TestBinaryDiscovery -count=1

# Updater: endpoint, prerelease skip, cache fallback, brew/manual upgrade commands
cd source/server && go test ./pkg/update

# Offline --version behavior (no network)
cd source/clients/cli && go test -run TestReleaseVersionOffline .

# Full suites
cd source/server && make test        # go test ./...
cd source/clients/cli && make test    # go test ./...

# Local builds with git-describe version
make -C source/server build
make -C source/clients/cli build

# Local dist rehearsal (unsigned), per unsigned-rehearsal.md
scripts/release-macos-archives.sh
```
