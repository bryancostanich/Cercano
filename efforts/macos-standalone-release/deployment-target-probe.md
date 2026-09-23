# Deployment-target probe (bounded, no production edits)

**Date:** 2026-09-22 · **Scope:** reproduce the release server build with `MACOSX_DEPLOYMENT_TARGET=12.0`, inspect the Mach-O metadata, run ONLY `--version` in isolation, and inspect the cached `go-keychain` dependency's API availability. No agent launch/contact, no credential inspection, no signing action, no publishing, no `dist/` archive touched, nothing committed.

## Reproduced build

Source of truth read: `scripts/build-macos-unsigned.sh` (server build at lines 55–59: `GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags "-X main.version=$VERSION" -o …/bin/cercano ./cmd/cercano`).

Fresh temp dir: `/tmp/cercano-dt-probe.aEPDU0` (created via `mktemp -d`, removed after the probe).

```bash
cd source/server
MACOSX_DEPLOYMENT_TARGET=12.0 GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
  go build -trimpath -buildvcs=false \
  -ldflags "-X main.version=0.0.0-target-probe" \
  -o "$PROBE_TMP/cercano" ./cmd/cercano
```

**Result:** BUILD_OK (go1.26.3 darwin/arm64; ~6s; 61,936,418-byte binary). Linker emitted 15 warnings:

```
ld: warning: object file (.../T/go-link-.../go.o) was built for newer 'macOS' version (26.0) than being linked (12.0)
ld: warning: object file (.../000000.o … 000014.o) was built for newer 'macOS' version (26.0) than being linked (12.0)
```

Environment: host macOS 26.5.2 (25F84), Xcode 26.6, SDK `MacOSX.sdk` with `SDKSettings` default deployment target 26.5. The Go compiler's own object files recorded min-OS 26.0 at compile time while the final link honored `12.0`.

## Mach-O evidence

- `lipo -info`: `Non-fat file: architecture: arm64`.
- `otool -l` → `LC_BUILD_VERSION`: **platform 1 (macOS), minos 12.0, sdk 26.5**, tool version 1267.0; `LC_SOURCE_VERSION 0.0`. The deployment target WAS applied to the linked binary.
- `otool -L` (only system dylibs; nothing vendored/bundled):
  - `/usr/lib/libresolv.9.dylib` (compat 1.0.0)
  - `/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation` (compat 150.0.0)
  - `/System/Library/Frameworks/Security.framework/Versions/A/Security` (compat 1.0.0)
  - `/usr/lib/libSystem.B.dylib` (compat 1.0.0)
- `codesign -dv` (read-only): linker ad-hoc signature (`flags=0x20002(adhoc, linker-signed)`, identifier `a.out`) — default linker behavior, not a signing action.

## `--version` run (only command executed)

```bash
HOME="$PROBE_TMP/home" XDG_CONFIG_HOME="$PROBE_TMP/xdg-config" \
XDG_CACHE_HOME="$PROBE_TMP/xdg-cache" XDG_DATA_HOME="$PROBE_TMP/xdg-data" \
TMPDIR="$PROBE_TMP" "$PROBE_TMP/cercano" --version
```

**Output:** `cercano v0.0.0-target-probe`, exit 0. Isolated HOME/XDG dirs remained empty after the run (no files written outside the temp tree; no keychain/credential access attempted). This ran on the macOS 26.5.2 host only — it does not exercise macOS 12 behavior.

## go-keychain cached dependency (`github.com/99designs/go-keychain v0.0.0-20191008050251-8e49817e8af4`, indirect via `github.com/99designs/keyring v1.2.2`)

Cache path inspected: `$(go env GOMODCACHE)/github.com/99designs/go-keychain@v0.0.0-20191008050251-8e49817e8af4`.

- Cgo bindings (`#cgo LDFLAGS: -framework CoreFoundation -framework Security`) in `corefoundation.go:6`, `keychain.go:10`, `macos.go:6`, `ios.go:6` — matching the `otool -L` output above.
- APIs bound: `SecItemAdd`, `SecItemCopyMatching`, `SecItemUpdate`, `SecItemDelete`, `SecAccessCreate`, `SecTrustedApplicationCreateFromPath`, `SecKeychainCreate/Open/Lock/Unlock/GetStatus/ItemDelete`. All 12 `Sec*` symbols appear as undefined symbols in the probe binary (`nm -u`), confirming go-keychain is actually linked (~204 keychain-related symbols present).
- SDK availability annotations: `SecItem*` functions are `API_AVAILABLE(macos(10.6)–(10.10))` (`SecItem.h:54–477`); `SecKeychain*`, `SecAccessCreate` (`macos.go:28` comment: "Only available in 10.10"), and `SecTrustedApplicationCreateFromPath` carry no availability gates — they predate the annotation system and remain in the macOS 26.5 SDK headers. **All go-keychain API requirements are ≤ macOS 10.10, comfortably below the 12.0 target** (deprecated-but-present legacy Keychain APIs).

## Compile vs. runtime — what this probe does and does not establish

**Established:** the build succeeds with `MACOSX_DEPLOYMENT_TARGET=12.0`; the linked binary records min-OS 12.0 and declares only system frameworks; `--version` runs on this host; every Security API used by go-keychain has availability ≤ 10.10, so that dependency introduces no post-12.0 requirement.

**NOT established:** actual macOS 12 runtime support. The Go 1.26.3 toolchain compiled its runtime/package objects with min-OS 26.0 and the linker explicitly warned about the 26.0-vs-12.0 mismatch; a successful link does not prove those objects avoid instructions or OS-gated behavior unavailable on macOS 12. Only a run on a real macOS 12 (arm64) machine — or an audited lower-target Go toolchain build — could establish supported runtime. No such machine was available/used; no macOS 12 runtime claim is made.

## Cleanup

Temp files removed after evidence capture: `rm -rf /tmp/cercano-dt-probe.aEPDU0 /tmp/cercano-dt-probe.path`. No repo files modified except this report; nothing committed.
