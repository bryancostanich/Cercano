# Cercano AI Organization Migration

## Problem and motivation

Cercano's GitHub repositories, release automation, Homebrew distribution, website, and local development work currently live under the personal bryancostanich namespace. Move the product to the existing cercano-ai organization and the local home at /Users/bryancostanich/git_repos/cercano-ai without losing unfinished work, breaking distribution, or broadening private access unintentionally.

This is an ownership and location migration, not a product redesign. Existing repositories must be transferred rather than recreated so their history, issues, pull requests, releases, and attachment-hosting context are retained.

## Goals

Transfer the seven identified repositories to cercano-ai: Cercano, Cercloud, homebrew-tap, homebrew-cercano, cercano-claude, cercano-codex, and cercano-gemini. Preserve their existing names and visibility. Cercloud remains private.

Make cercano-ai/homebrew-tap the canonical Homebrew repository and cercano-ai/tap the supported tap. The canonical installation command becomes `brew install cercano-ai/tap/cercano`. Homebrew has no separate organization registration; this namespace follows GitHub ownership. Preserve the shared tap's Lattice cask and its distribution while consolidating Cercano formula publishing into the canonical tap. Transfer the dedicated homebrew-cercano repository without deleting its history or silently creating a second independently maintained release destination. Clearly document its superseded status and the supported migration route.

Update owner-qualified release and updater endpoints, formula generation and publishing, plugin synchronization, website links, documentation, and applicable integration permissions. Existing installed Cercano clients must retain a working update path. Existing Homebrew users must have a tested, documented transition, without relying solely on GitHub redirects or requiring destructive uninstall/reinstall steps.

Move the three local Git repositories, their registered worktrees, and identified Cercano backup/document directories into /Users/bryancostanich/git_repos/cercano-ai. Preserve branches, unpublished commits, dirty and untracked files, registered worktree relationships, and non-origin remotes. Account for absent local checkouts of GitHub-only repositories when establishing the new local home; cloning these repositories must not substitute for preserving existing local state.

Restore and verify GitHub Pages under the new organization. With the existing repository name and no custom domain, the expected address is https://cercano-ai.github.io/Cercano/. Keep the /Cercano website base path unless verification demonstrates a required correction. Verify README video attachments continue to play; do not reupload media merely to change repository ownership.

## Inventory baseline

The read-only inventory identified seven GitHub repositories with administrator access and an empty destination organization and local directory. The organization is on GitHub Free with default repository permission read. Organization Actions policy inspection was denied because the current CLI token lacks the necessary organization administration scope. Changes to credentials or scopes require explicit approval.

Cercano has 51 registered worktrees, 110 local branches, and five worktrees with tracked changes or untracked entries. Cercloud has one checkout with six modified documents and three new documents. The shared Homebrew repository has two clean registered worktrees. Every registered path existed at inspection. Three additional standalone backup/document directories and nested worktrees also require preservation.

Ninety-eight Cercano branches have no upstream; 36 have commits not reachable from cached origin refs. These are preservation signals, not proof of unpublished functionality: some may represent rebased or historical backup commits. No branch may be deleted based on these counts.

Local main and GitHub main differ. The live GitHub repository has website deployment configuration absent from the inspected local main checkout. Refresh remote state and reconcile safely before preparing source changes; never overwrite a newer remote configuration with stale local content.

The core repository has release and github-pages environments, plugin synchronization, Homebrew publishing, and Apple signing/notarization configuration. Secret names were inspected, not values. The active Homebrew publisher targets the shared personal homebrew-tap, not the separate homebrew-cercano repository. Plugin synchronization explicitly targets three repositories under bryancostanich.

This baseline is time-sensitive. A fresh, durable inventory and preservation manifest must precede any mutation, and a final quiesced snapshot must precede filesystem relocation.

## Constraints and safety boundaries

No unfinished work may be lost, implicitly committed with migration edits, stashed without an agreed recovery procedure, or discarded. Do not delete, prune, reset, or automatically archive branches, worktrees, backup directories, or repositories. Preserve ignored files and local-only configuration during moves, not just tracked Git contents. Keep credentials out of inventory artifacts and logs.

Cercloud must remain private, with no unintended expansion of effective access. Inspect source permissions and destination organization policy before transfer. If the organization's default read permission would broaden access, stop for a focused authorization decision; do not silently change organization-wide policy or waive this invariant. Preserve fork and collaborator relationships where supported and verify relevant access after transfer.

Public ownership transfers, access-policy changes, credential authorization, publishing, and any compatibility-breaking choice require explicit execution confirmation. Approval of the plan does not authorize deleting old namespaces or relaxing security. Use GitHub's transfer facilities; never recreate repositories at old names as a compatibility shortcut because that can disrupt redirects.

Pause competing agents, release jobs, and other writers during their affected cutover windows. Record affected processes and obtain approval before stopping unrelated services. Relocate the checkout used by an active agent last or perform that relocation from a stable external working directory; do not invalidate the executing session mid-step.

GitHub redirects are a compatibility aid, not a substitute for updating canonical endpoints. Authenticated API redirects may be refused by existing tooling; preserve token confinement and artifact validation when changing paths. Keep old published binaries, release tags, artifact names, signatures, and checksums intact. Do not publish a new product release merely to test the migration without separate approval.

No automatic package/module/import renaming, product rebranding, repository-name cleanup, custom-domain migration, or retirement of legacy plugin support is included. Preserve active plugin synchronization unless its retirement is separately approved. External package registries, GitHub Apps, OAuth applications, and deployments remain verification obligations where discovered; do not presume that a repository transfer migrates them.

Move worktrees using supported Git relocation/repair procedures with recovery information recorded first. Do not assume a generic folder move or a fresh clone repairs worktree metadata, saved agent-session directories, editor workspaces, or absolute paths. Preserve topology and use deterministic mappings under the new root; unexpected collisions or external dependencies require a pause rather than improvisation.

## Decisions already agreed

The destination is the existing cercano-ai GitHub organization and local /Users/bryancostanich/git_repos/cercano-ai directory. No separate cercano GitHub organization or Homebrew namespace will be acquired.

All identified Cercano Homebrew assets are in scope. The canonical target is cercano-ai/homebrew-tap, yielding cercano-ai/tap. The shared repository's Lattice cask remains intact. Its unrelated functionality must not be removed as part of consolidation.

Transfer existing repositories rather than rebuilding them elsewhere. This preserves identity and history; copying code into newly created repositories does not meet the preservation requirements.

Keep GitHub ownership changes, integration updates, and local filesystem relocation in separately verified stages. Prepare coordinated updates before transfer, establish the working new remote ownership and delivery paths, and move local workspaces last. This limits simultaneous failure causes and retains a usable working location during remote cutover.

No additional architectural fork is needed to define this scope. Findings that violate the stated preservation or compatibility requirements become explicit execution blockers, not silently delegated policy choices.

## Acceptance criteria

All seven repositories are present under cercano-ai with expected history, visibility, issues, releases, and access. Relevant origin URLs use the organization while unrelated remotes remain intact. Old endpoints are tested for the compatibility behaviors on which installed clients depend.

The canonical Homebrew tap serves Cercano, retains Lattice, and is the single supported publication destination. Both fresh installation and migration from the old tap are tested in an isolated environment. The dedicated legacy tap has clear transition guidance without history deletion. Package publishing authorization works with the new ownership.

Targeted updater, formula/publishing, and workflow checks pass. Existing public release downloads resolve, signatures/checksums remain valid, and signing/release configuration is preserved. Any validation requiring an actual release or access unavailable to the agent is clearly reported rather than claimed complete.

The website builds with its existing Pages checks and deploys successfully at the new location. Internal links, asset paths, public README links, and all existing video attachment players work. Plugin sync destinations and credentials are verified without unintended content overwrites.

Every original local branch and worktree is accounted for under the new home, with preserved dirty/untracked/ignored contents and no missing registered paths. The migration manifest reconciles the before/after state. Required Git operations and targeted development commands work from the new paths, and path-dependent tools and sessions are either updated or explicitly listed for user action.

Provide a final report of repository URLs, local paths, Homebrew commands, verification results, outstanding manual requirements, and recovery references. Do not claim completion while a required compatibility or preservation check is unresolved.

## Approved scope revision — legacy Homebrew platforms

The user clarified that this is a new distribution and preserving the obsolete Intel Mac Homebrew path is not required. Canonical standalone distribution remains cercano-ai/tap with its actual currently released platform requirements. Do not delay cutover for legacy platform parity or implement an automatic legacy-platform upgrade bridge. The dedicated old tap becomes a clearly labeled superseded repository rather than an actively supported install destination; retain its history and existing release artifacts. Document unsupported platforms accurately, without implying new Intel/Linux standalone builds exist. No archival, deletion, or new platform release is authorized. Supported standalone installation and tap transition checks remain required. This supersedes earlier requirements insofar as they demanded continued support for the old dedicated tap.

## Approved verification exception — existing installer restart bug

The user approved tracking the unchanged v0.20.3 post-install restart failure separately and continuing the ownership migration. Issue: https://github.com/cercano-ai/Cercano/issues/55. Canonical download, installation, formula test and signature checks passed, but the post-install command failed; do not report a clean end-to-end install. This explicit exception supersedes that acceptance requirement only for this reproduced existing-release defect. Runtime changes and a new product release remain outside migration authorization.
