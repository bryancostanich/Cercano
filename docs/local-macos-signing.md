# Local macOS signing

`scripts/sign-macos-release.sh` signs disposable staging copies of `cercano`
and `cercano-cli` using Developer ID Application, hardened runtime, and secure
timestamps. This is the first local step of the release pipeline, not a complete
release build. Notarization, final archives/checksums, and clean-Mac verification
remain separate gates. Do not publish these staging files.

## Local rehearsal

Requires an Apple Silicon Mac, Xcode command-line tools (`lipo`, `codesign`), Go
for the build, and a valid Developer ID Application certificate with its private
key in your Keychain. Secure timestamping requires access to Apple's service.

From the release worktree root:

```bash
# Lists public identity metadata; does not export private keys.
security find-identity -v -p codesigning

# Copy the exact Developer ID Application name or its full 40-hex SHA-1 fingerprint.
export CERCANO_CODESIGN_ID='Developer ID Application: Your Name (YOURTEAMID)'
# Optional: otherwise macOS uses its configured keychain search list.
# export CERCANO_CODESIGN_KEYCHAIN="$HOME/Library/Keychains/login.keychain-db"

version=0.0.0-local
rehearsal=$(mktemp -d)
bash scripts/build-macos-unsigned.sh "$version" "$rehearsal"
tar -xzf "$rehearsal/cercano-$version-darwin-arm64-unsigned.tar.gz" -C "$rehearsal"
stage="$rehearsal/cercano-$version-darwin-arm64-unsigned"
bash scripts/sign-macos-release.sh "$stage/bin"

# These invoke only version output, not the running agent.
"$stage/bin/cercano" --version
"$stage/bin/cercano-cli" --version
```

The original unsigned archive and its checksum remain unsigned and unchanged.
The extracted directory's README still labels the rehearsal unsigned; it is not
release packaging. The signer modifies only its two staged binaries. It does not
install, restart the agent, notarize, publish, change Keychain permissions, or
export keys. `codesign` does use the selected private key through Keychain and
may request authorization. Never pass a directory containing live installed
binaries or hard links to them.

Both binaries are checked before signing starts: regular, executable, non-symlink,
arm64-only files. Identity selection must match exactly one valid Developer ID
Application name or fingerprint, never a partial name or an ad-hoc identity.
Each signature is strictly verified and checked for Developer ID authority,
hardened runtime and a nonempty secure timestamp. Any error stops the script.
Signing is not transactional: discard the staging directory on failure and
extract fresh copies before retrying.

## Password prompts: three separate issues

- **When exporting:** Developer ID certificates can be exported with or without
  a password. Passwordless exports are supported in the CI pipeline — leave
  `MACOS_CERTIFICATE_PASSWORD` unset. Base64 encoding is not encryption; GitHub
  secrets remain private even when unset.
- **When signing:** macOS may ask to unlock the Keychain or authorize `codesign`
  to use the certificate's private key. This pipeline does not bypass that
  authorization or change private-key access controls.
- **When running Cercano:** the agent reads provider credentials from Keychain.
  Stable signing identity and designated requirements can let macOS recognize
  rebuilt versions as the same application. Ad-hoc or unsigned rebuilds can
  break that recognition. Existing item access controls and Keychain lock state
  still matter, so signing alone does not guarantee prompt-free operation.

The existing permissive development helper is unchanged. The strict signer
retains normal basename-based signing identifiers rather than introducing new
identifiers. Signing disposable staging binaries does not fix the currently
running agent or the launcher you normally use. Verify the actual running build
and compare designated requirements across rebuilds before diagnosing recurring
runtime prompts:

```bash
codesign -d --verbose=4 /path/to/cercano
codesign -d -r- /path/to/cercano
```

Do not grant all applications access to credentials to suppress prompts. A real
signed-rebuild test using isolated test credentials is still needed; this script's
unit tests cannot establish Keychain trust continuity.

## Credential-free verification

```bash
bash -n scripts/sign-macos-release.sh
python3 scripts/test-macos-signing.py
```

The tests execute the script with isolated PATH shims for `uname`, `lipo`,
`security`, and `codesign`. They cover identity selection, keychain paths with
spaces, preflight errors, signing and verification failures, and missing signature
metadata. They never use real keys or Apple's services. Passing mocks is not proof
of an accepted Apple signature or notarization.

## Controlled build target and local notarization

The rehearsal builder now pins macOS 12.0 in both `MACOSX_DEPLOYMENT_TARGET`
and explicit CGO compiler/linker flags. Go does not key its C-object cache on
that environment variable alone. The builder rejects anything other than
arm64-only binaries with exact Mach-O `minos 12.0`. This is still a build target,
not proven macOS 12 runtime support.

Notarization requires credentials separate from the Developer ID signing key.
If you do not already have a named notarytool profile, run this interactively in
your own terminal (do not paste passwords or private keys into chat):

```bash
xcrun notarytool store-credentials cercano-local
```

Follow Apple's prompts for your chosen authentication method. For Apple ID
credentials use an app-specific password, not your normal account password.
The command validates with Apple and stores credentials in Keychain. The pipeline
does not create profiles or change Keychain permissions itself.

**Important:** Use an app-specific password that is distinct from your Apple ID
account password and your Developer ID certificate export password. App-specific
passwords are generated in your Apple ID account security settings.

After signing the staged binaries as above:

```bash
python3 scripts/notarize-macos-local.py "$stage/bin"   "$rehearsal/notarization" --keychain-profile cercano-local
```

This uploads both signed binaries to Apple. The output directory must not exist,
and its parent must exist. Optional `--keychain PATH` selects a nondefault
Keychain. No raw-password argument is accepted. The command preserves a submission
ZIP, SHA-256, submission ID when returned, tool stdout/stderr, and notarization log.
It also writes `diagnostic-report.json` — a sanitized summary of well-known
notarytool fields (`id`, `status`, `message`, return codes, timeout flags) with
credential-like values redacted — and prints that summary to stdout on failure,
so the outcome is diagnosable without the runner-only raw captures.
It reports success only after a successful Accepted response and a matching
Accepted log. It has a bounded wait (default 1800 seconds per request) and never
automatically resubmits. On failure inspect the printed history/info/log commands;
a timeout can mean Apple is still processing the original submission. JSON output
may omit the ID until completion, in which case recover it using history.

The submission ZIP is not a final release archive. Neither raw command-line
executables nor tar.gz archives support stapled notarization tickets. First-launch
Gatekeeper verification can therefore require network access to Apple. A clean-Mac
test is still required; successful notarization alone does not test credential
access or prove prompt-free upgrades.

Credential-free tests: `python3 scripts/test-macos-notarize.py`.
