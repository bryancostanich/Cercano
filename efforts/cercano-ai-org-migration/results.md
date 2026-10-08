# Cercano AI migration results

> **Correction:** `homebrew-tap` was returned to `bryancostanich/homebrew-tap` at the user's direction. It is the personal Lattice tap, not Cercano's canonical publishing destination. The historical results below describe the now-superseded migration assumption. Cercano release and Homebrew publishing are paused pending correction of the standalone tap destination, credentials and documentation. See execution-notes.md.


## Remote ownership and distribution

All seven existing repositories were transferred to https://github.com/cercano-ai with repository identities, branch/tag refs and release records verified immediately after transfer:

- Cercano
- Cercloud (private; Keith's read access explicitly approved)
- homebrew-tap (canonical; Lattice retained)
- homebrew-cercano (superseded; history/formula retained)
- cercano-claude
- cercano-codex
- cercano-gemini

Canonical new installation: `brew install cercano-ai/tap/cercano`.

Existing standalone installations can retain `bryancostanich/tap` through GitHub redirects. Isolated same-version upgrade verification passed without uninstalling or changing saved data. This does not claim a future-version upgrade or automatic receipt retargeting was tested. The user waived obsolete legacy Intel-platform compatibility; the dedicated tap README points to the canonical standalone distribution.

Website: https://cercano-ai.github.io/Cercano/ (HTTP 200). The old https://bryancostanich.github.io/Cercano/ address returns 404; GitHub repository redirects do not redirect that Pages site. Current site/source links were updated. All four README attachment downloads remain accessible and video rendering was verified during migration.

Lattice's cask was preserved byte-for-byte. Its actual v0.8 ZIP download and SHA-256 were verified after transfer; its personal-repository artifact URL remains intentional. An initial diagnostic guessed a DMG filename and returned 404; that was not the cask URL and is superseded by the actual URL/checksum verification.

## Publishing and verification

- Updated release/updater endpoints, formula rendering/publishing, plugin sync, packaged homepages, README and website links.
- Preserved artifact names/digests, signing boundaries, token confinement, optimistic locking, and formula content checks.
- Homebrew token replaced by the user in GitHub, never exposed in chat. Read-only publishing access verification passed for the tap and all three plugin repositories.
- Restored all workflows paused for cutover. No migration publisher intentionally remains disabled.
- Successful existing-release Homebrew recovery run: https://github.com/cercano-ai/Cercano/actions/runs/37818852829. It verified release provenance, downloaded the original build artifact, and reported `Homebrew tap unchanged: Cercano 0.20.3`.
- That replay initially exposed the archived formula's two old-owner URLs. A regression-tested, v0.20.3-only exact historical-template comparison accepts that immutable metadata while always emitting the canonical template. All other formula changes and legacy URLs for later releases remain rejected. No signed artifact or release tag was rewritten.
- Go updater/config tests and both server/CLI builds pass from the relocated checkout. Homebrew Python suite passes 59 tests; Ruby formula tests pass 6 tests. Pages/static export and rendered-site checks passed.
- Current plugin manifest/readme entry-point URLs were corrected and published after final audit; plugin functionality was not removed.
- No new product release was created for verification. Existing signatures and published artifacts were checked; future signing/notarization still depends on valid Apple credentials at release time.

## Accepted existing-release limitation

Issue https://github.com/cercano-ai/Cercano/issues/55 tracks the unchanged v0.20.3 post-install restart failure on an unrelated uninspectable process. The user approved treating it separately. Isolated installation laid down the package; formula tests and both binary signatures passed, but post-install returned an error. This is not recorded as a clean end-to-end installation pass.

The unchanged website dependency lockfile reports npm advisories. Dependencies were not upgraded during ownership migration. External account-level Apps/OAuth settings and destination organization secrets unavailable to this CLI were not independently exhaustively audited; known repository secrets, release settings, token access, website and publishing flows were checked. No broad CLI organization scope was added.

## Local home and preservation

New root: `/Users/bryancostanich/git_repos/cercano-ai`.

Moved 11 existing top-level directories, retaining names and structure, then cloned the four GitHub-only repositories. All seven repositories now have local checkouts. The seven original registered checkouts/worktrees were repaired and immediately verified for identical HEAD and working-file status. Subsequent intentional core rebase and tap fast-forward reconcile published migration work; backup refs retain the prior core main state. Cercloud's modified and untracked documents remain local and uncommitted. Other user work was not stashed, discarded or implicitly committed.

Moved original directories:

- Cercano
- Cercano-cross-platform-updates
- Cercano-org-migration
- Cercano-migration-homebrew-tap
- Cercano-rebase-backup-20260930_123519
- Cercano-reset-planning-backup-9dpb40ys
- Cercano-token-accounting-docs-8fuczyxf
- Cercano-worktrees
- Cercloud
- homebrew-tap
- homebrew-tap-cercano

Additional clones: homebrew-cercano, cercano-claude, cercano-codex, cercano-gemini.

All source files were hashed against the final snapshot immediately before same-filesystem renames. Git worktree repair updated metadata afterward. No old worktree deletion was performed; reductions in count were the user's explicitly approved cleanup. Original directories were moved, not replaced by fresh clones.

Updated `~/bin/cercano` to default to the new Cercano checkout, preserving its custom debug environment, build/restart behavior and arguments. Bash syntax passed. Updated local `.mcp.json` paths with case-insensitive prefix handling for this filesystem. Backed up and transactionally updated 974 stored conversation path fields whose new target directories exist; conversation text was not changed, and SQLite integrity passed. Historical references to already-removed directories were left intact rather than creating missing worktrees.

## Running-session clarification

The initial ChatGPT handoff diagnosis was wrong for this conversation. Parent-chain inspection proved current tool calls run through the installed agent in `~/bin/.cercano-libexec`, outside the move. Restored the unnecessary temporary MCP override before final backup. Only approved source-bound helper processes were terminated with SIGTERM; this installed agent and its ancestors were protected and remained running.

The approved move therefore completed without restarting this session. The next normal `cercano` launch uses the updated default checkout and its usual rebuild/restart behavior. Existing processes inherit old environment values until restarted; if manually exporting CERCANO_REPO, update/unset it. Reopen any old editor workspace or auxiliary client connection against the new path. Do not launch unattached stdio tool servers just to recreate stopped helper processes.

## Recovery evidence

Private backup root (not committed or uploaded):
`/Users/bryancostanich/git_repos/cercano-ai-migration-backups`.

Final verified pre-move snapshot:
`20261008T172810Z-live-preflight`.
Despite its reusable script's historical name, this snapshot was taken after source-bound writers were stopped and recorded zero changed entries and zero verification failures. It contains the path manifest, full source copies, refs, relocation log, launcher/config backups, SQLite backup/path-change manifest and post-move build results. Source file hashes were checked again immediately before moving.

Remote cutover evidence and earlier staged runtimes:
`20261008T044239Z-remote-cutover`.

Earlier live snapshot:
`20261007T232032Z-live-preflight` (provisional; includes the documented concurrent-file-version capture).

Backups and staging copies are retained. No cleanup, archival, secret revocation, old-namespace recreation or force-push was performed. Reverse ownership transfer and any restore that overwrites current work remain separately approved operations.
