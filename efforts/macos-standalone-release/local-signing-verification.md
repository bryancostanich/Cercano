# Local signing verification — 2026-09-22

User directed local-first signing before CI integration. Implemented a separate
strict staging signer, preserving the permissive development helper.

## Verified

- `bash -n scripts/sign-macos-release.sh`: passed.
- `python3 scripts/test-macos-signing.py`: seven unittest methods passed,
  including failure subcases. Tool shims never use real keys.
- `security find-identity -v -p codesigning`: one valid Developer ID Application
  identity, team `4HPM47RCM4`.
- Built both binaries with existing unsigned rehearsal script at version
  `0.0.0-local`, then signed only extracted disposable staging copies with the
  discovered identity. No keys exported or Keychain permissions changed.
- Both real `codesign --verify --strict` invocations passed.
- Both metadata outputs show Developer ID Application authority,
  `flags=0x10000(runtime)`, and Apple secure timestamps.
- Both signed binaries ran `--version`, reporting `0.0.0-local`.
- Designated requirements use identifiers `cercano` and `cercano-cli`, Apple
  Developer ID certificate constraints, and team `4HPM47RCM4`.

Staging binaries (temporary, not installed):
`/tmp/cercano-signing.F0pwBT/cercano-0.0.0-local-darwin-arm64-unsigned/bin/`

The tar.gz and checksum beside the staging directory remain **unsigned**.
No notarization, signed archive, release publication, CI provisioning, agent
restart, or live credential-read test was performed.

## Remaining gates

- Prompt-free runtime Keychain trust across rebuilds has NOT been verified.
  Signing-key authorization and runtime credential access are distinct.
- The unsigned rehearsal builder does not constrain deployment target. This
  agent reports VersionMin=1703936 (macOS 26.0), while the client reports
  VersionMin=786432 (macOS 12.0). Do not use this rehearsal as evidence for a
  macOS 12 support floor. The production release builder must set and verify
  the audited target, followed by actual supported-OS runtime verification.
- Notarization, final archive verification/checksums, redistribution notices,
  clean-Mac trust, and protected CI integration remain incomplete.
