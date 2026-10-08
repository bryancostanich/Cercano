# Remote cutover coordination checklist

Prepared locally, not published:

- Core migration branch `migration/cercano-ai-org`, worktree `Cercano-org-migration`, commit `b21ee75589eb`.
- Canonical tap migration branch `migration/cercano-ai-tap`, worktree `Cercano-migration-homebrew-tap`, commit `5768ae147191`.
- Planning/user commits on active main remain separate; do not overwrite them or reset main to the implementation branch.

## Preconditions and coordination

Before any remote mutation, obtain user confirmation to pause pushes, merges, releases and plugin/tap publishing across the affected repositories. Local editing may continue. Recheck currently running/queued Actions runs, source HEADs, source collaborators and destination membership immediately before transfer. If new remote commits appeared, reconcile the prepared changes and repeat affected tests before publishing; never force-push.

Current source collaborators and effective access must be preserved. Cercloud stays private. Organization default repository access is read: future organization members may acquire read access to private repositories under that policy. At preflight only the owner was a member. Recheck current membership; stop if effective access would broaden unexpectedly.

Destination organization secret/variable inventory and personal-account GitHub App installations could not be enumerated with existing credentials. Ask the user to identify any configured organization secrets or external Apps/deployments not visible to this run, or confirm none need migration. Do not claim those checks completed. Cross-repository token authorization remains unverified until the transferred destinations exist; never expose token values.

## Proposed sequence after explicit confirmation

1. Record exact pre-cutover workflow states and pause affected publishing workflows. Do not cancel running work without confirmation; wait or coordinate first.
2. Transfer dependency repositories: homebrew-tap, homebrew-cercano, and the three plugin repositories. Check each destination repository's identity, refs, visibility, collaborators and release/issue metadata. Keep publishers paused throughout.
3. Transfer Cercloud with private access unchanged, then transfer Cercano. Verify each result and old/new endpoint behavior before proceeding.
4. Update local origins once per shared repository, preserving unrelated remotes. Do not move any local paths.
5. Reconcile and publish approved core/tap migration commits only when destinations are available. Check the tap's new artifact URL actually downloads with the unchanged checksum before publishing its formula.
6. Mark the dedicated old tap superseded with a concise README pointing to `brew install cercano-ai/tap/cercano` and current Apple Silicon requirements; preserve history/artifacts. Do not archive or delete it.
7. Verify/reauthorize Homebrew and plugin publishing tokens through approved secret paths. If authorization is missing, keep that publisher paused and request the required credential action. Do not add broad org permissions automatically.
8. Validate Pages configuration and deploy using its workflow only after the new owner and prepared changes are in place. Verify website/README links and videos. No new product release is authorized merely for testing.
9. Restore workflows selectively after their gates pass. Report any publisher still paused and why.

## Verification prepared so far

Core updater Go tests passed; 58 Homebrew Python tests passed; 6 Ruby formula tests passed; Pages build and both static export tests passed; vinext build and all three rendered HTML tests passed. The first Pages test failure identified an obsolete expected domain and was corrected to the new site URL. Diff whitespace checks passed. Formula version, digest and behavior remain unchanged; Lattice cask is byte-identical to current upstream.

npm reported 29 dependency advisories (2 low, 2 moderate, 24 high, 1 critical) using the unchanged existing lockfile. No dependency upgrades or automatic audit fixes were made as part of this ownership migration. This finding is not a claim that deployed static pages expose all reported development dependency risks.

## Recovery boundaries

Repository transfer reversal requires a separately confirmed operation and may face GitHub naming/permission constraints; it is not an automatic rollback. Preserve old namespace redirects by not recreating old repositories. If an integration fails, leave its publisher paused, preserve evidence and apply an approved targeted correction. Do not publish references to missing artifacts, force-push source history, delete old releases, or reset user work. The approved private backup preserves pre-migration local refs and contents, but the live snapshot is not a substitute for the final quiesced relocation snapshot.

Local relocation remains a separate future coordination gate. Additional implementation worktrees must be included in the refreshed path map and final backup.
