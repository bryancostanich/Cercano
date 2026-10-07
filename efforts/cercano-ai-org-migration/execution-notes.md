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
