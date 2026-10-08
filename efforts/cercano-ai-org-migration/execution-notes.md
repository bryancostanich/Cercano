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

## Remote cutover approval and fresh access blocker

User authorized remote cutover and confirmed no pushes, merges or releases while execution proceeds. Local relocation remains separately gated. Fresh read-only checks found no active Actions runs in the seven repositories; destination names remain unoccupied and source administrator access is intact. Current main and prepared patches were inventoried without altering working trees.

New security finding: destination organization members are bryancostanich and keithballinger; Keith has active member (not owner) role. Organization default repository permission remains read. Cercloud source collaborators still contain only bryancostanich. Transferring Cercloud now would broaden effective private-repository access to Keith, contrary to the existing preservation constraint unless explicitly approved. Stop before transfer or policy change. Options: approve this specific read-access expansion, defer Cercloud while moving public repositories, or explicitly change organization base permission to none (broader policy change requiring its own approval). No workflows were paused and no remote writes occurred.

Fresh private evidence: /Users/bryancostanich/git_repos/cercano-ai-migration-backups/20261008T044239Z-remote-cutover/before.json and org-policy-recheck.json. The user cleanup authorization remains in force; absent old worktrees are not blockers.

## Private access approved

User explicitly confirmed "keith's access is intended" after being informed that the organization read default grants Keith access to transferred Cercloud. The transfer may proceed with Cercloud private and current organization policy unchanged. This approval is specific to the disclosed access consequences; do not grant unrelated additional privileges. Remote transfer/publication coordination authorization remains active; local relocation remains separately gated.

## Remote ownership and publication completed; Homebrew credential blocker

Transferred all seven repositories to cercano-ai. Verified stable repository IDs, private/public visibility, all branch/tag refs and release IDs immediately after each transfer. Cercloud remains private and Keith's approved permission is read. Core has 50 preserved branch/tag refs and 18 preserved releases. Evidence and exact event log are in the private remote-cutover snapshot recorded above.

Paused four publishers after confirming no active runs: Pages, release, Homebrew and plugin sync. Updated origins for the three shared local repositories without moving directories. Rebased core migration patch cleanly over 16 newer upstream commits; reran updater, Python Homebrew, Ruby formula and Pages tests successfully. Did not alter user main or unrelated working files.

Published core namespace migration c46d7e2e and canonical tap 5768ae147191, preserving artifact version/digest and Lattice. Downloaded the new formula URL and verified its SHA-256 against the unchanged formula (42,488,877 bytes). Added legacy-tap supersession guidance in remote commit 68c1d5636de04151b4c100273b856b7f86c5fa22; no existing README was present, and history/formula remained intact.

Published manual read-only credential verification workflow in core commit 5092ec50a4ee. It uses existing stored secrets only, refuses redirects, checks fixed github.com API destinations and never prints token values or writes target repositories. Syntax and mocked success/denial/missing-token tests passed. Run 37729829269 reports all three plugin repositories writable by PLUGIN_SYNC_TOKEN but HTTP 403 for HOMEBREW_TAP_TOKEN against cercano-ai/homebrew-tap. Retained secret storage is verified; Homebrew authorization is not. No scopes or secret values were changed.

Reenabled and dispatched Pages; run 37729733696 succeeded. https://cercano-ai.github.io/Cercano/ responds 200, and all four README video attachments respond 200 as video/mp4. Old and new latest-release API endpoints respond 200; old owner endpoint redirects to the stable repository ID. Attachment issue #35 and environment/repository secret names remain present.

Reenabled plugin sync following its read-only credential checks. Pages and plugin sync are now enabled; release workflow 371451543 and Homebrew workflow 372635319 remain disabled pending Homebrew credential repair. User must authorize or replace HOMEBREW_TAP_TOKEN for the new organization, using a repository-scoped token with Contents read/write for cercano-ai/homebrew-tap (and any required organization approval), then update the existing secret in Cercano's release environment. Do not paste values into chat. Re-run the verification workflow before restoring these publishers. Existing metadata check does not claim an actual plugin write or release has been tested.

Local directories remain unmoved. Full isolated installation/upgrade tests and final local snapshot/relocation are still outstanding; the run is not complete.

## Replacement token verified; publishers restored

User confirmed saving the replacement token. Manual non-writing access check run 37730690949 passed. Reenabled release workflow 371451543 and Homebrew workflow 372635319; all previously paused publishers now report active. No credential values were retrieved or exposed. This verifies reported repository write permission, not a new product release or target-content write.

## Isolated installation exposed existing runtime failure

Created an isolated Homebrew prefix, HOME, configuration, cache and logs under the private remote-cutover snapshot's isolated-homebrew-check directory. Copied the installed Homebrew runtime into that private prefix; confirmed brew --prefix matched the isolated path before installation. A bound, non-listening loopback port was placed in the isolated Cercano configuration to avoid contacting the user's running agent. The host Homebrew installation and user config were not modified. Package-manager dependencies were fetched into the isolated tree.

Canonical tap clone/discovery succeeded. brew install fetched the unchanged v0.20.3 artifact, installed both binaries and linked the isolated keg, but returned 1 because restart-after-upgrade failed during process discovery: inspect candidate PID 1494: inspect PID 1494: no such file or directory. Homebrew additionally emitted an error-serialization stack trace and a post_install deprecation warning; these are not evidence of namespace failure.

brew test subsequently passed; codesign --verify --strict passed on both installed binaries. A direct probe of the isolated binary with an explicit bound non-listening loopback endpoint reproduced the same PID inspection failure. ps identifies PID 1494 as an unrelated same-user ChatGPT for Chrome extension-host process. Source inspection shows process discovery skips ESRCH but propagates other inspection errors before determining ownership. No code fix has been applied; further root-cause work is needed before altering this security-sensitive discovery logic. The artifact's verified unchanged hash establishes this is not caused by the namespace patch.

The installer gate is blocked: do not claim a clean fresh install. Ask whether to explicitly accept this existing-release limitation for the ownership migration and track a separate fix, or expand scope to diagnose/fix and authorize a new signed release. A new product release remains prohibited without explicit approval. Local relocation has not started, and the running agent was not restarted.

## Installer exception accepted and issue filed

User approved the recommendation and requested an issue. Filed https://github.com/cercano-ai/Cercano/issues/55 with observed reproduction, successful checks, unresolved deterministic trigger, and process-ownership safety requirements. Record the installer task as completed with this explicit accepted exception, not as a successful post-install test. Continue migration without modifying runtime behavior or publishing a new release.

## Existing-tap compatibility and local relocation preflight

Verified old bryancostanich/tap GitHub URL clone into the isolated Homebrew prefix. Temporarily modeled an existing standalone receipt by setting its source tap to the old name (fixture only; restored original afterward). `brew upgrade bryancostanich/tap/cercano` reported v0.20.3 already installed and exited 0. Binary hash, isolated config and a saved-data sentinel stayed unchanged. This is a same-version compatibility test, not a future-version upgrade or an actual receipt retargeting test. Published precise guidance in tap commit 56a5a10c4b35; no uninstall required. The isolated test also fetched Homebrew core into its private prefix, not the host prefix.

Fresh relocation map contains 11 top-level source directories mapped by unchanged basename into /Users/bryancostanich/git_repos/cercano-ai. There are 7 registered checkouts/worktrees: 3 Cercano, 3 shared tap, 1 Cercloud. Include 3 standalone backups and the Cercano-worktrees container. No mapped destination exists. Additional migration worktrees are included. Map is saved privately in the remote-cutover snapshot as local-relocation-map.json, and the latest temporary copy is /tmp/cercano-local-relocation-map.json. Refresh after further cleanup as approved.

Processes with source-root cwd or loaded files include VS Code Plugin PID 1610, Cercano PID 41710 (executable source/server/bin/cercano), and related node/dotnet/MCP processes. This session cannot simply move its own executable/working directories while related tools continue writing. These PIDs are observations, not kill authorizations; re-identify before any stop. No processes were stopped.

Staged byte-identical copies of both current runtime binaries under the private remote-cutover snapshot/stable-runtime. SHA-256 checked against source; not launched and not substituted into PATH or the active service. This prepares a stable handoff but does not establish that the active agent has switched. Obtain local pause and handoff coordination, then verify the old source-root processes are gone, take the final quiesced snapshot, and relocate/repair Git worktrees. User must approve stopping affected sessions; do not terminate unrelated editors/tools or broaden shutdown scope implicitly.

Local move is blocked at its explicit coordination gate. Remote transfers and publication do not authorize filesystem relocation or process termination by themselves. All source directories remain in their original locations.

## Local pause approved; precise tool-server handoff required

User said "ok, ready", authorizing the coordinated local move window and affected process handoff. Fresh process inspection corrected an earlier assumption: listener PID 51798 on port 50052 is the installed singleton at /Users/bryancostanich/bin/.cercano-libexec/cercano with cwd /Users/bryancostanich. It is already outside the source tree and must not be stopped just for this move.

Source-bound PID 41710 is a separate Cercano tool server parented by ChatGPT, with source-root cwd and MCP-related child processes; another ChatGPT node process also has the old source cwd. This tool-server connection needs a client-side launch configuration change and reconnect before a genuinely quiesced snapshot. Do not assume this is the normal CLI singleton or kill the installed listener.

The staged server/client binaries still exactly match the current source binary hashes. Use the staged server path /Users/bryancostanich/git_repos/cercano-ai-migration-backups/20261008T044239Z-remote-cutover/stable-runtime/cercano with the connector's existing arguments/environment, and a working directory /Users/bryancostanich/git_repos/bryan_costanich (the parent directory is not moving). Reconnect/restart the client so its tools no longer hold the source checkout. Verify fresh process paths after reconnect before final snapshot or moves.

A probe `cercano agent --help` was not a help-only command: it attempted server startup and exited because port 50052 was occupied. It did not replace the listener. Startup logs were written; no successful second service was started. Future help inspection must use source or top-level --help rather than assuming subcommand flag behavior.

No local directories have moved and no processes were terminated. This is an external client configuration/reconnection blocker, not a request for renewed transfer or move approval.

## Client launch configuration prepared locally

At user request, inspected the project .mcp.json and found its Cercano command exactly matches the running source-bound server. The parent process is ChatGPT's embedded Codex executable; the user .codex/config.toml has no separate Cercano server definition. This strongly identifies the project config as a relevant launch source, but the next reconnect must verify that the client reloads it rather than retaining cached configuration.

Backed up .mcp.json privately in the remote-cutover snapshot, changed only the Cercano command to stable-runtime/cercano-mcp-handoff, and preserved arguments/environment and all other servers. Wrapper changes cwd to the nonmoving parent directory then execs the byte-identical staged server. JSON structural comparison and sh -n validation passed; wrapper was not launched. This is a temporary local launch override, not a portable project setting for publication. Restore/update it to the relocated permanent path after successful relocation. Do not commit the temporary absolute backup path.

Client reconnect/restart remains required: executing a new stdio server in a tool subprocess cannot attach it to the existing client connection. No source-root server or child processes have been terminated.

## Local relocation completed; corrected host identification

User identified the active interface as the normal Cercano launcher. Tracing the actual RunCommand process ancestry proved it descends from the installed agent, not the unrelated source-bound ChatGPT helper. Reverted the temporary .mcp.json override byte-for-byte before final backup. The earlier external-client handoff blocker was withdrawn. Only the approved source-root helper processes received SIGTERM; installed/current-session ancestors were explicitly protected. No source cwd/executable consumers remained in the recheck.

Final snapshot 20261008T172810Z-live-preflight captured all 11 current source directories without errors or changing entries; all regular-file hashes matched again immediately before rename. Same-filesystem moves placed them under /Users/bryancostanich/git_repos/cercano-ai. git worktree repair succeeded; all seven original checkouts/worktrees initially retained exact HEAD and status. Four additional transferred repositories were cloned, making all seven repositories and eleven registered checkouts/worktrees available locally. Missing old worktrees were skipped per prior cleanup approval.

Updated ~/bin/cercano's default source path only; preserved custom debug environment and all build/restart logic, backed up the script, and passed bash -n. Updated .mcp.json's old Git_Repos case variant to the new server path. Backed up the conversations database with SQLite's backup API, then transactionally updated 974 project/development path fields with existing destination directories. Historical references to absent directories were left unchanged. Conversation content was not edited; integrity checks passed. Saved path metadata and script/config backups remain private.

Core and tap local main histories were reconciled without force-pushing, stashing or overwriting user work. Source builds for both server and CLI and updater/config tests pass at new locations. The active installed session remains running. Stopped external stdio helpers are not auto-launched unattached: reopen affected clients/workspaces against the new path when needed. No temporary binary switch or ChatGPT handoff is required for this session. The next ordinary cercano launcher invocation uses the new checkout; existing processes retain inherited environment until restarted.

## Actual recovery pipeline validation

A manual recovery dispatch with an obsolete input name was rejected before starting; inspecting the live workflow established its version/run_id interface. Correct dispatch run 37818190585 verified provenance and checksum but exposed the exact two-owner-URL mismatch between the immutable v0.20.3 archived formula and current canonical template. Downloaded the original artifact and proved those are the only differences.

Implemented a regression-tested, v0.20.3-only exact historical-template comparison, keeping canonical output and all archive/provenance/checksum/same-version/tampering checks. Commit a09aa6962ac3 published. Python suite passes 59 tests; real archived artifact preparation yields byte-identical canonical formula. Retry https://github.com/cercano-ai/Cercano/actions/runs/37818852829 succeeded and logged Homebrew tap unchanged. No new release or artifact rewriting occurred.

Final audit updated and published plugin README/manifest owner references (bfae0a49de8b, 2640fa194715, 26ad04f573e4). Remaining old-owner references are intentional historical docs/fixtures, legacy formulas, the v0.20.3 compatibility case, or Lattice's unchanged artifact source. Actual Lattice ZIP checksum verified. Old Pages URL is 404; new URL is 200. All workflow states are active.

Completion and honest verification boundaries are summarized in results.md. Task completion includes the user's explicit legacy-platform and issue #55 exceptions and the reported manual/external verification limits; it does not assert a new signed release was performed or every account-level integration was visible. Backups and migration worktrees are retained, with no cleanup or archival.

## Correction: personal Homebrew tap returned

The user clarified that homebrew-tap belongs to bryancostanich because it distributes Lattice, and its Cercano material is deprecated. This supersedes the earlier canonical-tap migration assumption. Transferred the existing repository back to bryancostanich/homebrew-tap, preserving ID 1197609489, visibility, branch/tag refs and the Lattice cask blob. No force-push or formula/history deletion. Updated the shared local origin; no local directories moved in this correction.

Paused Cercano release workflow 371451543 and Homebrew publisher 372635319 after verifying no active runs, to prevent writes through the former organization URL redirect. They remain paused until the actual standalone publishing destination and credential authorization are corrected. Existing installation documentation and the dedicated homebrew-cercano supersession notice from the mistaken migration also need correction; do not treat them as authoritative pending the target decision. No replacement tap was created or repurposed without confirmation. Private evidence: /Users/bryancostanich/git_repos/cercano-ai-migration-backups/tap-return-20261008T122443.

## Correction: approved destination is the dedicated homebrew-cercano tap

The user approved the standalone publishing destination: `cercano-ai/homebrew-cercano`, with the canonical installation command `brew install cercano-ai/cercano/cercano`. `bryancostanich/homebrew-tap` is personal (Lattice) and its Cercano formula is deprecated; its formulas and refs were not touched in this correction. This supersedes both the earlier `cercano-ai/homebrew-tap` canonical-tap assumption and the dedicated repo's interim supersession notice.

Work was staged in isolated worktrees without pushing or merging. The dedicated repo's standalone-inapplicable v0.9.3 formula was replaced with the exact tested current standalone `Formula/cercano.rb` from the local homebrew-tap checkout (unchanged v0.20.3 digest and release URL), and its erroneous superseded README was replaced with current standalone install instructions including the Apple-Silicon/macOS-12 caveat and known issue #55. Baseline dedicated refs were captured before edits: `main`/`origin/main` at 68c1d5636de04151b4c100273b856b7f86c5fa22; history is retained.

Core publisher destination, token verification workflow, promote/readme/release-build/install/website command references were updated from `cercano-ai/homebrew-tap`/`cercano-ai/tap` to the canonical dedicated repo and `cercano-ai/cercano/cercano` install command. The v0.20.3-only former-owner template compatibility case, old-updater fixtures, Go module IDs and Lattice URLs were deliberately left unchanged.

Remaining gate: the actual `HOMEBREW_TAP_TOKEN` grant must be re-issued or re-scoped to `cercano-ai/homebrew-cercano` and verified with the read-only verify-publishing-access workflow before release workflow 371451543 and Homebrew publisher 372635319 are re-enabled. No token value was created or accessed, no workflow enabled, no release published, and nothing pushed in this correction; the user must perform the token grant and publication.
