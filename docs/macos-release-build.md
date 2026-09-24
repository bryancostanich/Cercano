# macOS release build

`scripts/build-macos-release.sh` produces the signed Apple Silicon release
archive that the Homebrew formula refers to. It builds both binaries from one
tag-derived version, verifies them, signs them through
`scripts/sign-macos-release.sh`, and packages exactly the layout
`release/homebrew/render_formula.py` accepts:

```
cercano-<version>-darwin-arm64/
  bin/cercano
  bin/cercano-cli
  LICENSE
  README.txt
```

## Usage

```bash
export CERCANO_CODESIGN_ID='Developer ID Application: Your Name (YOURTEAMID)'
bash scripts/build-macos-release.sh 1.2.3 dist
```

This writes `dist/cercano-1.2.3-darwin-arm64.tar.gz` and a `.sha256` file
beside it.

## What it refuses

The script fails closed, and a refused build leaves no archive behind:

- **Unsigned builds.** `CERCANO_CODESIGN_ID` must name a Developer ID
  Application identity; `none` and `-` are rejected. There is no unsigned
  escape hatch — use `scripts/build-macos-unsigned.sh` for rehearsals.
- **Unstable or untagged versions.** Only `X.Y.Z` without a leading `v` or
  leading zeros, and a matching `v<version>` tag must exist. Set
  `CERCANO_ALLOW_UNTAGGED=1` for a local dry run only.
- **Wrong architecture or a raised deployment floor.** Both binaries must be
  arm64-only with a macOS 12.0 build target. This is checked before either
  binary is signed.
- **Version mismatch.** Each binary must report the version being built, so an
  archive cannot claim a version its contents do not.
- **Signing or post-staging verification failure.** Signatures are re-verified
  on the exact bytes that get archived.
- **Existing artifacts.** It will not overwrite an archive or checksum that is
  already present, so a published artifact cannot be silently replaced.

## What it does not do

The script **does not notarize**. A built archive is signed but not yet
distributable: run `scripts/notarize-macos-local.py` (or the release workflow)
against the staged `bin` directory, and complete the clean-Mac rehearsal,
before promoting anything to the tap. It also does not push, publish, tag, or
touch the tap, and it bundles no models, runtimes, or credentials.

## Verifying a finished archive

`scripts/verify-macos-release.py` inspects an archive the way a consumer
receives it, rather than trusting the machine that produced it:

```bash
python3 scripts/verify-macos-release.py dist/cercano-1.2.3-darwin-arm64.tar.gz \
    --version 1.2.3 \
    --sha256 <expected-64-hex-digest>
```

It checks the archive layout, the SHA-256 digest, arm64-only architecture, the
macOS 12.0 deployment target, a Developer ID Application signature with
hardened runtime and a secure timestamp, executable permissions, the required
`LICENSE` and `README.txt`, and that each binary reports the expected version.
Any failure exits non-zero and says not to publish.

**How notarization is checked.** A bare Mach-O executable cannot carry a
stapled ticket — `xcrun stapler` handles bundles, disk images and installer
packages, not loose binaries. So notarization is confirmed the way Gatekeeper
does it, by assessing each binary with `spctl --assess`, which consults Apple's
online records and must see `source=Notarized Developer ID`. **This requires
network access.** Offline, pass `--skip-gatekeeper`; the run then reports
notarization as `UNVERIFIED` rather than implying success.

Accepted-submission evidence from `scripts/notarize-macos-local.py` can be
passed with `--notarization-evidence <dir>`. That is corroborating provenance
— it records that *a* submission was accepted — not proof that these exact
bytes are notarized. Only the Gatekeeper assessment establishes that.

## Tests

```bash
python3 scripts/test-macos-release-build.py
python3 scripts/test-macos-release-verify.py
```

Both suites drive the scripts with stub `go`, `lipo`, `otool`, `codesign`,
`spctl` and signing executables on `PATH`. They prove refusal behavior, archive
layout and permissions, agreement with the formula renderer, and that every
verification check actually fails when its property is violated. They cannot
prove that a real toolchain, a real Developer ID signature, or real
notarization succeeds; those need the real build, a networked Gatekeeper
assessment, and the clean-Mac rehearsal.
