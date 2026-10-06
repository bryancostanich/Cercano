# Release formula and upgrade

`cercano.rb.in` is the Apple Silicon release formula template. It is **not ready to publish**: replace `@RELEASE_URL@`, `@VERSION@`, and `@SHA256@` with the final signed/notarized release archive's values. The archive must contain `bin/cercano` and `bin/cercano-cli`; its agent binary must implement `restart-after-upgrade`. Do not attach this hook to the legacy 0.8.1 single-binary formula.

## Rendering the formula

`render_formula.py` fills the template's three placeholders from a verified local archive:

```bash
python3 release/homebrew/render_formula.py \
    --version 1.2.3 \
    --archive dist/cercano-1.2.3-darwin-arm64.tar.gz \
    --sha256 <expected-64-hex-digest> \
    --output dist/cercano.rb
```

It hashes the archive bytes before parsing the tar, requires that digest to equal the one you supply, then checks layout without extracting or executing anything: exactly `bin/cercano` and `bin/cercano-cli` as regular executable files under a single `cercano-<version>-darwin-arm64` directory, with only `LICENSE` and `README.txt` permitted beside them. Symlinks, hardlinks, devices, FIFOs, absolute or traversing paths, duplicate members, extra executables and stray files are rejected. The URL is built only from the validated version and archive name. The output is created exclusively and never overwritten, and any validation failure writes no formula at all.

**Trust boundary.** This renderer checks archive layout and byte digest only. It does **not** verify code signatures, notarization, Mach-O architecture, the macOS deployment target, or the version the binaries actually report, and it never downloads, installs, executes archive contents or publishes. A rendered formula is not evidence that signing or notarization passed.

The unsigned rehearsal build (`scripts/build-macos-unsigned.sh`) emits a `-unsigned` archive and top-level directory, so it is deliberately rejected; only a final release archive renders.

## Homebrew Integration

Homebrew runs `post_install` after placing/linking the new keg. The hook invokes the **new keg's absolute executable**, not a PATH lookup. Installation itself only places files. The coordinator does nothing when no owned agent listens at the configured endpoint. It does not register a background service or compare client/agent versions.

For an existing owned agent, the coordinator captures launch settings, validates local restart prerequisites, acquires the existing client auto-launch lock using the agent's original temporary directory, rechecks ownership, requests shutdown through the existing RPC, waits for the actual process to exit, launches the new executable, and holds the lock until the replacement's listener and gRPC connection are ready. The existing agent determines its normal bounded drain/cleanup behavior. No forced-kill fallback is added.

Failures before shutdown leave the existing agent alone. Failures during/after shutdown are reported; they cannot promise that the old agent is still running. An unsuccessful restart does not roll back the installed files. Inspect logs before retrying:

```
/absolute/new/keg/bin/cercano restart-after-upgrade
```

The default endpoint is `127.0.0.1` with the port from Cercano configuration. If the running agent was started with an environment-only `CERCANO_PORT` override not inherited by Homebrew, supply its address explicitly when retrying:

```
/absolute/new/keg/bin/cercano restart-after-upgrade --address 127.0.0.1:50053
```

Automatic discovery of agents on other ports is not implemented. A nil ownership result describes the selected endpoint, not all agents on the machine. Nonstandard launch arguments are refused instead of replayed unsafely.

## Promoting to the tap

`promote_to_tap.py` stages a rendered formula into a local checkout of
`bryancostanich/homebrew-tap`:

```bash
python3 release/homebrew/promote_to_tap.py dist/cercano.rb ~/git/homebrew-tap --version 1.2.3
```

Promotion is what makes a release reachable by users, so it refuses to run
until the referenced artifact is **actually downloadable and matches the
checksum the formula claims**. A formula pointing at a missing or altered asset
would break `brew install` for everyone. It also rejects unrendered
placeholders, a version or URL that disagrees with the expected version, a tap
path that is not a git checkout, invalid Ruby, and a formula identical to what
the tap already has.

`--skip-download` skips the artifact check for offline work; the run then
reports the artifact as `UNVERIFIED` rather than implying it was checked.

**The tool never commits, pushes, or opens a pull request.** It writes one file
into a local checkout. Review the diff and publish deliberately.

## Testing

```bash
cd release/homebrew && python3 -m unittest test_render_formula test_promote_to_tap
ruby release/homebrew/test_formula.rb
```

The promotion tests serve the artifact from a loopback HTTP server, so the real
download and digest-comparison path runs; no live release or tap is involved.
Every refusal is asserted to leave the tap checkout unmodified.

The Python tests build tiny inert tar fixtures and cover valid rendering, digest/name/version mismatches, incomplete archives, hostile members (symlink, hardlink, FIFO, traversal, absolute path, smuggled executable, stray file, second tree), duplicate members, refusal to overwrite an existing formula, and templates with missing, duplicated or unknown placeholders.

The Ruby tests exercise our formula methods against a small DSL stub. Neither suite proves Homebrew's actual lifecycle, sandbox behavior, detached-process survival, or installation of a real release archive; those need the clean-Mac rehearsal.

Nothing in this directory publishes to the tap.

## Automatic tap update after publication

The release workflow's **Update Homebrew tap** job runs `update_tap.py` after
successful publication, never for a rehearsal. It verifies the local formula
against the release template and the published archive against the build digest,
then uses optimistic locking to update only `Formula/cercano.rb`. Repeated
identical updates succeed without a commit; older versions and same-version
changes are refused.

This remote updater is separate from `promote_to_tap.py`, which remains a
local-only staging tool. See [release setup and recovery](../../docs/macos-release-build.md#automatic-tap-update-after-publication)
for the scoped `HOMEBREW_TAP_TOKEN` setup and retry instructions.

```bash
python3 -m unittest discover -s release/homebrew -p 'test_update_tap.py'
```

Updater tests use simulated GitHub responses; no live tap writes are performed.
