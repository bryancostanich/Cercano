# First unsigned macOS arm64 archive

## Build and verification

From the release worktree root:

```sh
bash scripts/build-macos-unsigned.sh 0.0.0-rehearsal dist/macos-unsigned
python3 scripts/test-macos-unsigned.py \
  dist/macos-unsigned/cercano-0.0.0-rehearsal-darwin-arm64-unsigned.tar.gz \
  0.0.0-rehearsal
```

Both commands completed successfully on the macOS arm64 development host with Go 1.26.3 and CGO enabled. The build does not use the development signing helper or inspect signing credentials. It builds in each Go module with `-trimpath -buildvcs=false -ldflags '-X main.version=0.0.0-rehearsal'`. No release tag or public version was created. This is a repeatable build procedure, not a claim of bit-for-bit reproducibility or source provenance attestation.

Artifact: `dist/macos-unsigned/cercano-0.0.0-rehearsal-darwin-arm64-unsigned.tar.gz`

Size: 42,166,953 bytes.

SHA-256: `b7d79159dd3ccffccebf34642edfcc3338df1e92b7adf90872e6ba6d236bce8b`

The sibling `.sha256` contains the digest and archive basename. Generated `dist/` artifacts are ignored by git.

## Archive contents

One versioned root directory contains `bin/cercano`, `bin/cercano-cli`, `LICENSE`, and `README.txt`. Known Go-embedded catalogs, skills, tokenizer data, configuration defaults, database schema, and web-search script compile into the binaries; see build-inputs.md. No model weights, private configuration, provider credentials, development launcher, or third-party runtime executable is staged by this script.

This is not yet proof of a complete base-runtime asset inventory. Optional runtime downloads, Python-backed research dependencies, and third-party license notices still need release review before publication.

## Checks performed

- Verified archive checksum and exact expected regular-file contents; rejected links and unexpected members in the smoke-test extractor.
- Extracted into a temporary directory outside the source tree.
- Verified both executables are arm64 with `lipo`.
- Ran `cercano --version`, `cercano version`, and `cercano-cli --version`; all returned the exact expected version with matching binary names.
- Used isolated HOME and XDG directories with a minimal environment. Version commands left HOME empty. No agent was launched, no live singleton was contacted, and no model was downloaded by these version tests. Separate command regression tests already intercept HTTP to establish offline version behavior.
- Inspected `otool -L`: both binaries link only to system libraries/frameworks (libSystem, libresolv, CoreFoundation, Security); no Homebrew dylib paths were present.
- Verified rerunning the builder refuses existing artifacts and invalid path-like versions are rejected before building.
- Two initial script failures exposed a missing output-directory creation and incorrect module working directory; both were corrected before the successful build.

## Important limitations

`codesign -dvv` reports `Signature=adhoc` and no TeamIdentifier for both binaries. “Unsigned” here means no Developer ID distribution signature and no notarization; Go/toolchain ad-hoc signatures may be present.

`otool -l` reports **minos 26.0 / SDK 26.5 for the agent**, and **minos 12.0 / SDK 12.0 for the CLI**. These are observed build defaults, not the supported release baseline. A deliberate deployment target, dependency/runtime validation, and clean-machine tests are required before publishing. Do not present this archive as compatible with older macOS versions.

Not performed: full installed startup, configured-model interaction, compatibility/upgrade verification, complete licensing review, Developer ID signing, notarization, Gatekeeper verification, GitHub Actions publication, or formula installation. Nothing was pushed or published, and no active agent was restarted.
