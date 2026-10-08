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

**How notarization is checked.** Notarization evidence from
`scripts/notarize-macos-local.py` is **required**, passed with
`--notarization-evidence <dir>`. Each binary's code directory hash (cdhash) is
compared against the cdhashes in Apple's Accepted notarization ticket. A match
proves those exact bytes were notarized. Offline, pass `--skip-gatekeeper`; the
run then reports notarization as `UNVERIFIED` rather than implying success.

Two other approaches were tried against a real notarized build and rejected:

- **`spctl --assess`** only evaluates app bundles. It rejects a correctly signed
  and notarized command-line binary with *"does not seem to be an app"*.
- **`codesign --test-requirement==notarized`** is unreliable for loose Mach-O
  files. Two freshly notarized probe binaries both failed it even though Apple
  returned Accepted and their cdhashes appeared in the ticket, and the failure
  persisted after waiting. A bare binary carries no stapled ticket — `xcrun
  stapler` handles only bundles, disk images and packages — so that lookup
  cannot be depended on.

The cdhash comparison is deterministic and needs no network call at
verification time, since Apple's ticket contents are captured at submission.

## Release workflow (GitHub Actions)

`.github/workflows/release-macos.yml` runs the whole pipeline on an Apple
Silicon runner: test gate, signing-identity import, build and sign, notarize,
verify the finished archive, render the formula, and upload artifacts for
review.

**It is manually triggered only.** There is no push, tag or pull_request
trigger, so untrusted code can never reach the signing credentials. An operator
starts each run from the Actions tab with a version, and publication is a
separate checkbox that defaults to off — a rehearsal stops at reviewable
artifacts.

> The previous tag-triggered `release.yml` was removed. It published unsigned,
> cross-compiled binaries automatically on any `v*` tag, which would have raced
> this pipeline and attached unsigned macOS artifacts to the same release.
> Nothing publishes without an explicit operator-initiated run. If Linux or
> Docker artifacts are wanted again, they need their own workflow under the
> same no-automatic-publish rule.

### Selecting the rehearsal source

Dispatch the workflow from a ref resolving to the exact commit tagged
`v<version>`. Both workflow validation and the release builder reject a tag
that exists on a different commit; an old release tag must never label a build
of the current branch. Start with `publish` unchecked. Creating/pushing the
ref and configuring the protected environment are separate operator steps.

### Required configuration

Create a **protected `release` environment** in the repository settings with
required reviewers, so a run cannot reach Apple credentials without human
approval. Add these secrets to that environment:

| Secret | Contents |
|---|---|
| `MACOS_CERTIFICATE_P12` | Base64-encoded Developer ID Application certificate (`.p12`) |
| `MACOS_CERTIFICATE_PASSWORD` | **Optional** password for that `.p12`. Leave unset for passwordless export. |
| `APPLE_ID` | Apple ID for app-specific password authentication |
| `APPLE_TEAM_ID` | Apple Developer Team ID |
| `APPLE_APP_SPECIFIC_PASSWORD` | App-specific password for Apple ID authentication |

The workflow creates a temporary keychain, resolves the signing identity from
it (failing if absent or ambiguous rather than trusting a configured string),
and **destroys the keychain and all decoded key material on every path**,
including failure and cancellation. Default permissions are read-only; write
access is scoped to the publish job alone. Publication refuses to replace an
asset already attached to the tag and re-checks the artifact digest before
uploading.

**Passwordless certificates:** If your Developer ID certificate was exported
without a password, leave `MACOS_CERTIFICATE_PASSWORD` unset or empty. The
workflow will notice and proceed with passwordless import. Base64 encoding is
not encryption — GitHub secrets remain private even when unset.

### Triaging a failed notarization

When the `Notarize` step fails, the run uploads a
`cercano-<version>-notarization-diagnostics` artifact containing **only**
`diagnostic-report.json`, the sanitized summary generated by
`scripts/notarize-macos-local.py`. It holds the well-known notarytool fields
(`id`, `status`, `message`), return codes and timeout flags for the submit and
log calls, with credential-like values redacted before anything leaves the
runner. The same summary is printed to the job log. A failure is therefore
diagnosable from the artifact alone — previously the generic "Submission did
not return a successful Accepted result" plus runner-only capture files left
nothing to investigate after the runner was recycled.

The raw `submit-*`/`log-*` stdout/stderr captures, the submission ZIP and the
`submission-id.txt` file stay on the ephemeral runner; the keychain,
certificates (`.p12`, `.p8`) and authentication parameters are never uploaded.
Apple's `message` may reference the submitting Apple ID or file paths, but the
app-specific password exists only inside the temporary keychain, which the
unconditional cleanup step destroys on every path.

When reporting a failed notarization, attach the sanitized report and the job
log — never paste certificates, keychain contents, app-specific passwords or
the `xcrun notarytool store-credentials` command line. The script never
resubmits; recover the submission ID from `xcrun notarytool history` and check
`info`/`log` before any manual retry, because a timeout may mean Apple is still
processing the original submission.

## Tests

```bash
python3 scripts/test-macos-release-build.py
python3 scripts/test-macos-release-verify.py
python3 scripts/test-release-workflow.py
```

The build and verify suites drive the scripts with stub `go`, `lipo`, `otool`,
`codesign`, `spctl` and signing executables on `PATH`. They prove refusal
behavior, archive layout and permissions, agreement with the formula renderer,
and that every verification check actually fails when its property is violated.

`test-release-workflow.py` checks the workflow's structure: manual-only
trigger, least-privilege permissions, protected environment, step ordering,
keychain cleanup on every path, verification before publication, and that no
workflow publishes on push or tag.

None of these prove that a real toolchain, a real Developer ID signature, real
notarization, or an actual Actions run succeeds; those need the real build, a
networked Gatekeeper assessment, and the clean-Mac rehearsal.

## Automatic tap update after publication

Publication-enabled runs now finish with **Update Homebrew tap**. This job runs
only after the release publication job succeeds; rehearsals (`publish=false`)
never write to the tap. It updates only `Formula/cercano.rb` on
`cercano-ai/homebrew-tap`'s `main`, with no pull request or approval prompt.

Before writing, the updater checks the CI archive and regenerated formula,
then downloads the publicly published macOS archive and its checksum sidecar.
Both must match the digest recorded by the build. Nothing is extracted or run.
An identical installed formula is a successful no-op. A newer version, or a
same-version change of digest/formula, is refused. Concurrent edits use the
Contents API's blob-SHA precondition; a conflict causes up to three fresh
read/validate attempts, never a force update. Other tap files are untouched.

### One-time credential setup

Create a **fine-grained personal access token** at
<https://github.com/settings/personal-access-tokens/new>:

- Resource owner: `bryancostanich`.
- Repository access: **Only select repositories**, then **homebrew-tap**.
- Repository permission: **Contents: Read and write**. Metadata read access
  is implicit. Do not grant workflow, administration, or all-repository access.
- Choose an expiration and renew before it expires.

Add it to Cercano's `release` environment as **HOMEBREW_TAP_TOKEN**:
<https://github.com/cercano-ai/Cercano/settings/environments>.
Enter it directly in GitHub, not chat, source, or a command-line argument.
The workflow supplies it only to the formula-update step. It uses the normal
read-only Actions token for release metadata and no credentials for public
artifact downloads. Authenticated API redirects are refused.

### Recovery

A missing/expired token, unavailable artifact, or concurrent conflicting change
fails the tap job **after** the release is already published. The release stays
published; do not delete/recreate its assets. Correct the issue and use Actions'
**Re-run failed jobs** (only the tap job should have failed), or rerun that
individual job. Build output digests and artifacts belong to the same workflow
run; retention is 14 days, so recover promptly. Do not rerun all jobs to repair
a tap-only failure: publication deliberately refuses replacing existing assets.

A downgrade refusal means a newer release already reached the tap. Leave it
alone. Same-version formula changes require deliberate manual review rather
than an automated overwrite. If the tap's branch protections forbid direct
commits, explicitly configure an allowed automation identity rather than
bypassing protections.

This automation requires the workflow change on the selected release commit
and the secret provisioned. It does not retroactively update earlier releases
such as v0.20.3, nor has a live cross-repository write been verified by offline tests.

### Tap-only update for an already-published release

For a release that predates automatic tap updates, run the dedicated recovery
workflow on current `main`, supplying its successful release run ID:

```bash
gh workflow run update-homebrew.yml --repo cercano-ai/Cercano --ref main \
  -f version=0.20.3 -f run_id=36933561441
```

This checks the published tag's commit against the completed release workflow,
requires successful macOS signing and publication jobs, and downloads the macOS
artifact from that exact run. It then performs the same checksum/template/public
asset verification and guarded tap update. It does not build, sign, retag,
republish assets, or update Windows/Linux packages. The source Actions artifact
must still be within its retention period. This workflow shares the automatic
tap job's concurrency group and uses the same narrow token.
