# Local signed rehearsal — 0.11.0

First end-to-end exercise of the real pipeline: build, sign with the actual
Developer ID identity, notarize with Apple, and verify the finished archive.
Run locally rather than in CI, because the GitHub runner still needs a
certificate export that turned out to be unnecessary for local work.

## What ran

Version `0.11.0` (next after the existing `v0.10.0` tag), built with
`CERCANO_ALLOW_UNTAGGED=1` so no tag was created on this branch.

1. `scripts/build-macos-release.sh 0.11.0` — both binaries built, verified
   arm64-only with a macOS 12.0 target and a matching reported version, then
   signed with `Developer ID Application: bryan costanich (4HPM47RCM4)`.
2. `scripts/notarize-macos-local.py` — submitted both signed binaries to Apple.
   Submission `48677c81-386c-4ff5-ad26-85871c00b387`, status **Accepted**,
   using the pre-existing `cercano-local` keychain profile.
3. `scripts/verify-macos-release.py` — passed after the correction below.

Archive digest: `dbab6854f62b92f8aa644df20e373a54dc6f77c498a264ed180d47da2f971b65`.

## The notarization check was wrong, twice

The rehearsal existed to catch exactly this, and it did.

**`spctl --assess` cannot verify bare binaries.** The first verifier rejected
the notarized `cercano` with *"the code is valid but does not seem to be an
app"*. `spctl` evaluates app bundles; a command-line executable is not one.
This would have failed every release regardless of correctness.

**`codesign --test-requirement==notarized` is unreliable for loose Mach-O
files.** It initially looked correct: the notarized `cercano` passed, an
unsigned control failed, and — importantly — a binary signed with the *same*
Developer ID but never submitted failed with exit 3 while passing a plain
signature check. That appeared to prove it distinguished notarization from mere
signing.

But `cercano-cli` failed it, despite Apple returning Accepted. Investigation:

- Apple's submission log listed **both** binaries in `ticketContents`.
- Both recorded cdhashes matched the files on disk exactly
  (`54380ef0…` for `cercano`, `1813bafb…` for `cercano-cli`).
- The failure was reproducible, not a propagation delay, and persisted after
  waiting.
- A fresh two-binary probe (copies of `/bin/echo` and `/bin/cat`, signed and
  submitted together) was Accepted by Apple with both cdhashes in the ticket —
  and **both** then failed the requirement check, including after 120 seconds.

So the check is not dependable for loose executables. A bare binary carries no
stapled ticket (`xcrun stapler` handles only bundles, disk images and
packages), so there is nothing local to consult, and the online lookup did not
behave consistently.

**The fix: compare cdhashes against Apple's ticket.** The notarization log
records the cdhash of every binary Apple accepted. Comparing each binary's
actual cdhash against that set is deterministic, needs no network call at
verification time, and proves those exact bytes were notarized.

Notarization evidence is now **required**; without it the verifier refuses
rather than quietly skipping the check. `--skip-gatekeeper` still reports
`UNVERIFIED` for offline runs.

## Verification

- Real notarized archive: **passes** all checks.
- Non-vacuity, probed directly: a tampered ticket (cdhash replaced) fails with
  *"these bytes were not notarized"*; omitting evidence fails with a refusal to
  claim notarization.
- `scripts/test-macos-release-verify.py`: 23 tests pass, rewritten around
  cdhash matching, including a binary absent from the ticket, a log for a
  different submission, an unaccepted log, a missing log, an empty ticket, and
  a signature with no cdhash.
- Workflow (20), build (11), notarize (10), formula and promotion suites: pass.

## Not yet done

No tag was created, nothing was pushed, and nothing was published to GitHub
Releases or the tap. The CI path has still never run: the workflow's signing
step needs `MACOS_CERTIFICATE_P12` and `MACOS_CERTIFICATE_PASSWORD`, which are
the only remaining unconfigured secrets. `APPLE_ID`, `APPLE_TEAM_ID` and
`APPLE_APP_SPECIFIC_PASSWORD` are configured in the protected `release`
environment.

A clean-Mac install rehearsal — `brew install` from the tap on a machine
without a development checkout, then `brew upgrade` against a running agent —
has not been attempted and remains the last substantive gap before release.
