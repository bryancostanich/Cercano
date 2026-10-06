# macOS signing audit

Source: `source/server/scripts/codesign-if-available.sh`. Verified against the source after delegated extraction; initial extracted line numbers and timestamp wording were corrected.

- Lines 19–20: use `CERCANO_CODESIGN_ID` when supplied; `none` explicitly disables signing.
- Lines 21–25: otherwise select the first Developer ID Application identity returned by `security find-identity -v -p codesigning`; silently succeed without signing if no identity is selected.
- Line 27: invoke `codesign --force --sign "$id" "$bin"`. The script uses `set -euo pipefail` (line 15), so an actual signing command failure is not ignored.
- Lines 2–7: the documented purpose is stable signing identity for development Keychain access across rebuilds, not a complete publishing pipeline.

## Release gaps

The helper does not fail closed when an identity is absent, explicitly request hardened runtime (`--options runtime`) or secure timestamps (`--timestamp`), verify the resulting signature in a separate step, or submit/check notarization. Absence of an explicit timestamp flag alone does not establish whether `codesign` supplies a timestamp by default. Identity discovery uses the valid-identities filter, but this helper is not a comprehensive release-certificate validation gate.

Preserve this permissive development behavior; implement strict release checks separately. Actual certificate availability, Apple notarization, and Keychain behavior across published upgrades remain untested.
