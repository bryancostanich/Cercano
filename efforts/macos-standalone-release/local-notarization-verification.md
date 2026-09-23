# Controlled build and notarization preparation — 2026-09-22

## Build probe and real rehearsal

The probe reproduced newer-macOS object warnings by reusing a Go cache populated
without a deployment target, then linking with MACOSX_DEPLOYMENT_TARGET=12.0.
Adding explicit CGO_CFLAGS and CGO_LDFLAGS containing
`-mmacosx-version-min=12.0` invalidates the relevant cache entries. A full server
build then produced minos 12.0 with no newer-target linker warnings.

The builder now applies these flags to both modules and verifies exact arm64 /
LC_BUILD_VERSION minos 12.0 before archiving. An initial metadata parser incorrectly
looked for LC_VERSION_MIN_MACOSX; an actual build exposed this and otool confirmed
LC_BUILD_VERSION. Corrected and repeated the full build successfully.

Real rehearsal output:
`/tmp/cercano-target-signing.fts8do/cercano-0.0.0-local-darwin-arm64-unsigned/bin/`

Both staged binaries were signed with the existing Developer ID identity. Strict
signature verification, hardened runtime/timestamp checks and both --version
invocations passed. The archive beside these extracted files remains unsigned.
No installation or running-agent changes were performed.

## Notarization implementation

`scripts/notarize-macos-local.py` verifies source binaries and private staged
copies, submits a two-binary ZIP using a named Keychain profile, requires Accepted
submission and matching Accepted log, and preserves diagnostics/IDs/checksum.
Failure and timeout never trigger automatic resubmission. No credential setup,
Keychain ACL changes, stapling or publication is performed by the script.

Verification:
- Shell syntax check for builder passed.
- Seven signing tests passed.
- Ten notarization tests passed, including failure subcases, using isolated PATH
  shims. Test review rejected an earlier ineffective mock approach; replacement
  tests execute the real script in subprocesses and cannot invoke real Apple tools.
- Local `xcrun notarytool log --help` and `store-credentials --help` confirmed CLI
  argument shapes and interactive credential setup.

## Blockers and limitations

Real local notarization succeeded on 2026-09-22 using the user-created
`cercano-local` Keychain profile. Both signed binaries passed script preflight;
Apple returned Accepted and the script verified the matching Accepted log.
Submission ID: `4eb0131d-ecc4-473e-b378-84326d8d8b62`.
Diagnostics, submission ZIP, checksum, and acceptance record are retained at
`/tmp/cercano-local-notarization-20260922` (temporary local storage).
Nothing was published or stapled; the ZIP is not final release packaging.
No Keychain credential contents were inspected.
No macOS 12 machine was used; target metadata does not establish runtime support.
Prompt-free Keychain access across signed rebuilds, final packaging, redistribution
notices, clean-machine Gatekeeper checks and CI integration remain pending.
