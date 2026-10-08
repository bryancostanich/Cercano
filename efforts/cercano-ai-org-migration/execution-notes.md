# Migration execution notes

## Initial preflight

Approved spec and plan saved in local commit 7415794cf7d0; not pushed. No implementation worktree, fetch, transfer, source migration, access change, publishing, or relocation has been performed by this run.

Authenticated GitHub identity has administrative access to all seven source repositories and active administrator membership in cercano-ai. Destination repository probes returned 404 for all seven names. Source Cercloud remains private and its collaborator listing contains only the owner. The organization member listing currently contains only the owner; organization default permission remains read. Future membership/access implications and full destination policies remain to be checked before transfer. Source Cercano has an additional collaborator whose write permission must be preserved.

A direct read-only local refresh completed after a delegated inventory stopped without completing its task. Treat the delegated partial file as incomplete; the direct output is local-preflight-complete.json. Git optional locks were disabled. No workspace modifications were performed by the inventory.

Current counts differ materially from the approved baseline: Cercano has 9 registered worktrees, Cercloud 1, and the shared Homebrew repository 2, for 12 total rather than 54. Four current worktrees have status entries (including intentional planning-document updates in the main checkout). Standalone backup/document directories and the Cercano-worktrees container are still present. Source candidate root paths report about 2.70 GiB allocated and the destination filesystem about 1341.56 GiB free. This is not a verified logical backup size; filesystem sharing and ongoing work may affect it.

The earlier /tmp/cercano-migration-inventory.json is no longer present, preventing a complete path-by-path comparison against the old snapshot. The historical inventory and approved spec recorded 51 core worktrees; fresh Git metadata reports 9. Do not assume cleanup was authorized or that missing workspace content can be discarded. Pause for confirmation of intentional cleanup or the location of relocated workspaces before establishing a new baseline or creating preservation backups.

Current evidence is temporary, access-restricted, and not itself a recovery backup:

- /tmp/cercano-org-migration-preflight/github-preflight.json
- /tmp/cercano-org-migration-preflight/local-preflight-complete.json

The local manifest records refs, worktree metadata, cached tracking relationships, status including untracked files, and collapsed ignored-directory entries. It does not hash all workspace contents. It must be retained in an approved durable location and refreshed during the final quiesced snapshot before relocation. No backup location has yet been approved or backups created.

## Cleanup confirmation

The user confirmed: "yes, i've been cleaning them up". The reduction from the historical worktree count is intentional activity outside this run. Use a freshly recorded current registry as the preservation baseline; do not recreate removed worktrees. Continue preserving all remaining work, including standalone backups and unregistered candidate directories. Refresh again before the final quiesced snapshot because cleanup and development may continue.

## Confirmed baseline refresh

After cleanup confirmation, a fresh direct inventory reports 7 Cercano worktrees, 1 Cercloud checkout, and 2 shared tap worktrees: 10 in total. All registered paths exist and all status/ignored-file reads succeeded. The current manifest includes refs, redacted remotes, dirty/untracked status, and collapsed ignored entries. Dirty workspaces are Cercano (including planning documentation), Cercano-release-macos, and Cercloud. Four non-repository candidates remain: the three standalone backup/document directories and the Cercano-worktrees container.

The historical 54-workspace task is satisfied against the user's explicitly revised current-state baseline, not by asserting the removed workspaces were preserved. Cleanup may still continue; refresh before final preservation.

The authoritative refreshed manifest is /tmp/cercano-org-migration-preflight/local-baseline-confirmed.json, mode 0600. A delegated refresh did not complete and was superseded by this verified direct inventory. Process cwd inspection found a VS Code plugin process using the main checkout. This narrow check is not a complete open-file or saved-session inventory and does not establish that relocation is safe; no processes were stopped. Evidence: /tmp/cercano-org-migration-preflight/process-cwd.json.

## Coordination still required

A durable backup location remains subject to user approval. Propose /Users/bryancostanich/git_repos/cercano-ai-migration-backups, outside both the source paths and the destination being moved into, with a uniquely timestamped snapshot directory and owner-only permissions. This would be a same-machine rollback copy, not protection against disk failure. Backups must include Git metadata and remaining ignored/untracked contents, which may contain sensitive local configuration. They must not be committed or uploaded.

Recovery procedure: retain a complete metadata-preserving snapshot and path manifest; verify archive/file readability and representative restoration into an isolated temporary location without touching originals. Before relocation, pause affected writers, refresh the snapshot, and validate preservation. If relocation fails, pause writers, preserve the failed state for diagnosis, restore the snapshot to the documented original paths, and repair/verify worktree metadata before resuming. Any overwrite during recovery requires explicit approval. Backup tooling and verification must detect files changing during capture; a live provisional snapshot is not accepted as a quiesced recovery point.

No repository transfer or relocation is authorized by the cleanup confirmation. Inventory metadata in /tmp is temporary and is not a verified recovery backup.

## Backup authorization and baseline revision

The user approved /Users/bryancostanich/git_repos/cercano-ai-migration-backups and instructed that further removed worktrees be skipped because cleanup is ongoing. Refresh the current registry and candidate directories before copying; exclude already-absent paths without another cleanup approval. Record disappearances and concurrent changes during capture explicitly rather than treating a live partial snapshot as a verified quiesced recovery point. Preserve every remaining workspace and do not initiate cleanup. This revises the preservation baseline only; transfer, publishing, access, and relocation gates remain unchanged.

## Approved live backup and recovery check

Created the approved owner-only backup root and timestamped snapshot:
`/Users/bryancostanich/git_repos/cercano-ai-migration-backups/20261007T232032Z-live-preflight`.

Copied all 13 remaining candidate source directories, including Git metadata and ignored/untracked files. Verified 54,783 regular-file hashes. One cross-platform-updates test file changed during verification (source size 6486 bytes versus captured 6470 bytes); kept the original captured version and stored the newer version separately with a stable-read check and hash. No source files were changed. This is a provisional live capture, not a quiesced point-in-time recovery baseline.

Restored the core repository into an isolated backup-owned recovery-check directory. Restored refs match the metadata manifest and `git fsck --full --no-reflogs` passed. Did not operate on restored linked-worktree pointers, which still reference original paths. A full quiesced snapshot and worktree recovery verification remain prerequisites for relocation.

Fetched origin without pruning for the three repositories after preserving pre-fetch metadata. Cercano main was 8 ahead/16 behind, Cercloud synchronized, shared tap 2 behind at that snapshot. No working-tree reconciliation was performed; preserve active user branches and prepare source changes in an isolated worktree from current upstream later. Fetch summary is stored privately with the snapshot.

## Integration inspection and authorization blocker

Saved integration-preflight.json in the private snapshot. All seven source repositories report Actions enabled with all actions allowed; no repository rulesets or hooks were returned. Core environments include github-pages with branch policy and release without protection rules. Repository secret names were inspected without values; plugin publishing uses PLUGIN_SYNC_TOKEN. Remaining environment/organization secrets, branch protections, OAuth/external services and package checks are not yet complete.

Destination organization Actions policy inspection returns HTTP 403. Current CLI OAuth scopes are admin:public_key, gist, read:org, repo, workflow; they lack admin:org. No authorization changes have been made. Destination organization currently has no teams or GitHub App installations. Personal-account installation enumeration also returned 403 because that endpoint requires GitHub-App user authorization; expanding admin:org alone will not solve that separate check.

Request either manual confirmation of destination Actions settings (least additional credential privilege) or explicit approval to expand CLI authorization with admin:org and complete the interactive browser authorization. That scope is broader than this one organization and must not be added implicitly. Manual review of source-account installed GitHub Apps and other externally configured integrations remains necessary unless supported alternate read access becomes available. No transfers or publishing have happened.

## Destination Actions policy evidence supplied

The user supplied three screenshots. Initial broad image extraction returned contradictory radio-button selections and was not accepted as evidence. Focused single-control inspections established: Allow all actions and reusable workflows selected; Read and write permissions unselected; Read repository contents and packages permissions selected; Allow GitHub Actions to create and approve pull requests ticked. These are manual screenshot observations, not successful API reads. No policy changes or credential scope expansion were made. The destination Actions-policy evidence blocker is cleared; other integration inventory requirements remain open.

Checked current upstream core workflows via GitHub read API. Pages explicitly grants contents:read, pages:write and id-token:write. Release defaults to contents:read and explicitly grants contents:write only to its publication job. Homebrew workflows use the separate HOMEBREW_TAP_TOKEN, while legacy plugin sync uses PLUGIN_SYNC_TOKEN. Thus read-only default workflow-token permissions do not by themselves require broadening the destination default for these publishers. Cross-repository token authorization must still be checked after transfer; screenshot evidence does not verify those credentials.

## Additional read-only integration and tap findings

Current main branches of all seven source repositories report protection disabled. Release environment secret names were inspected: APPLE_APP_SPECIFIC_PASSWORD, APPLE_ID, APPLE_TEAM_ID, HOMEBREW_TAP_TOKEN, MACOS_CERTIFICATE_P12. No environment variables were returned for release; github-pages has no environment secrets or variables. MACOS_CERTIFICATE_PASSWORD is referenced by the workflow but not listed as an environment secret; no claim of broken signing is made because an empty password or alternate configuration may be intentional. Values were not read. Destination organization secret/variable enumeration remains inaccessible with HTTP 403; record as unverified, not empty. Evidence is in integration-preflight-followup.json in the approved private snapshot.

Compared current remote tap formula contents. Shared homebrew-tap is version 0.20.3, installing the two-binary standalone archive and supporting macOS 12+ ARM64 only. Dedicated homebrew-cercano is version 0.9.3, installing a single co-processor-era binary with macOS ARM64/AMD64 and Linux AMD64 artifact branches. Canonicalizing on the already-agreed shared tap does not provide platform parity for existing legacy-tap installations. Both formulas currently declare MIT; do not rewrite old release licensing merely based on current source licensing without checking the applicable release.

Lattice cask is version 0.8 and references bryancostanich/lattice release artifacts/homepage. Those endpoints must remain unchanged: the Lattice application repository is not being transferred by this effort.

Before implementing tap migration behavior, obtain a decision: preserve the dedicated legacy formula and its existing platform-specific install paths, offering an explicit opt-in migration to the standalone formula only on supported Apple Silicon Macs; or hold the Homebrew cutover until standalone platform parity is available. The first is recommended because it avoids destructive or incompatible implicit upgrades and does not expand this migration into a new platform-release project. This would mean 'existing-installation compatibility' for legacy Intel/Linux means retaining their old artifacts, not promising standalone upgrades. No tap changes, repository transfers, or publication have been performed.

## Legacy compatibility decision resolved by user

User: "this is all new. we dont need to preserve the intel mac stuff. those are super old at this point". Proceed with the canonical standalone formula and current platform requirements; do not build a legacy upgrade bridge or wait for platform parity. Preserve history/artifacts and label the old dedicated tap superseded, without claiming unsupported platforms work. Record this as a spec revision, not a silent relaxation of acceptance criteria. No new publication or deletion authorization is implied.

## Prepared migration slice and next coordination gate

Created isolated core and tap implementation worktrees from origin/main without reconciling the active user main checkout. Core commit b21ee75589eb prepares updater, provenance, publishing, plugin sync, docs and website namespace changes. Tap commit 5768ae147191 prepares canonical identity/formula URLs while preserving version, digest, restart behavior and Lattice byte-for-byte. Nothing was pushed.

Verification: Go updater tests, 58 Homebrew Python tests, 6 Ruby tests, Pages build plus 2 tests, vinext build plus 3 tests, whitespace checks passed. Website initial failure was a stale old-domain expectation, corrected after observing rendered new-domain output. Existing dependency audit warnings are recorded in cutover-checklist.md; no dependency upgrades were made.

Remote cutover checklist now records dependency order, workflow coordination, access caveats, exact prepared commits, artifact checks, rollback limits and outstanding manual external-integration inventory. Pause for user confirmation of remote-writer coordination and transfer/publication authorization. This gate does not authorize local relocation.

## Secret screenshot confirmation

User-provided screenshot img_bafd57_4 shows the previously inventoried environment secret names (APPLE_APP_SPECIFIC_PASSWORD, APPLE_ID, APPLE_TEAM_ID, HOMEBREW_TAP_TOKEN, MACOS_CERTIFICATE_P12) and repository secret PLUGIN_SYNC_TOKEN. No values were requested or exposed. These are repository/environment-level entries, not evidence of destination organization-level secret configuration. GitHub transfer documentation explicitly states secrets remain associated with transferred repositories; verify their presence after transfer rather than requesting re-entry in advance. Stored GitHub token authorization may still require adjustment for new organization ownership. Apple credential values do not ordinarily depend on the GitHub owner. Screenshot does not authorize remote cutover; writer-pause and transfer/publication confirmations remain outstanding.
