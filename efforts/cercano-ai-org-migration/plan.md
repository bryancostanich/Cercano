# Cercano AI Organization Migration

Execute the approved spec.md. Transfer existing repositories into cercano-ai, establish cercano-ai/tap, restore integrations, and relocate local work last. Planning approval does not authorize destructive cleanup, broader private access, a new release, or unreviewed publication. Obtain the explicit confirmations specified below. Preserve all unrelated work. Record evidence and completion status as execution proceeds; never store credentials in the effort artifacts.

## Phase 1 — Refresh inventory and establish recovery

Objective: replace the historical inventory with a durable, current baseline before mutations. Files: effort execution notes and sanitized manifests, with complete local preservation backups outside repositories at a user-approved location. Tests: repository/worktree enumeration, remote reachability, backup integrity, and local recovery checks. Do not fetch into or modify checkouts during this phase until their pre-fetch state is recorded.

- [x] Confirm the execution checkout and locate the approved planning commit; use a dedicated migration worktree for code changes after approval.
- [x] Record GitHub identity, destination organization membership, repository visibility, effective permissions, and destination-name availability for all seven repositories.
- [x] Record source commit IDs, refs, tracking relationships, remotes with credentials redacted, dirty tracked files, untracked and ignored files, and registered worktree metadata.
- [x] Include all 54 baseline checkouts/worktrees, nested worktrees, standalone backup/document directories, and any newly discovered Cercano workspaces.
- [ ] Inventory release jobs, local agents and servers, editor workspaces, saved session working directories, and other writers or absolute-path consumers.
- [x] Establish a backup location and recovery procedure; preserve Git refs and metadata as well as dirty/untracked/ignored files. Git bundles alone are not a complete workspace backup.
- [x] Verify backup readability and representative recovery outside active workspaces without exposing secrets.
- [~] Fetch current remote refs after recording the baseline; classify divergence and reconcile only migration prerequisites, preserving unfinished branches and files.
- [ ] Record any conflicts or ownership ambiguity as blockers rather than discarding, stashing, or committing user work implicitly.

## Phase 2 — Verify permissions and integration prerequisites

Objective: ensure transfer and publishing can succeed without changing private access or bypassing organization policies. Files: execution notes and integration matrix; no secret values. Tests: read-only access checks, workflow inventory, source/destination policy comparison, and documented authorization checks.

- [ ] Compare Cercloud's existing effective access with destination organization defaults and teams.
  - [ ] Stop for a focused decision if the move would broaden private access; do not silently alter organization-wide permissions.
- [ ] Verify GitHub plan and organization policy implications for private repositories, protections, environments, and Actions.
- [~] Inspect source and destination Actions permissions, protected branches/rulesets, environments, deployment restrictions, variables, and repository/environment/organization secret names.
- [ ] Inventory Homebrew publishing and plugin sync token authorization, including organization approval requirements; request explicit approval for necessary credential or scope changes.
- [ ] Verify Apple signing/notarization configuration and record required environment protection and release settings without reading secret values into logs.
- [ ] Inventory GitHub Apps, webhooks, OAuth integrations, external deployments, and package/container registries where present; record transfer-specific requirements or access blockers.
- [ ] Establish a per-repository cutover order and pause window that prevents release/plugin/tap jobs from writing to half-migrated destinations.
- [ ] Confirm recovery limits: a reverse transfer is not assumed to be instant or policy-free, and restoring source files does not restore external settings.

## Phase 3 — Prepare coordinated compatibility changes

Objective: prepare reviewed changes against current upstream before public cutover, without pointing the live product at missing destinations. Files: source/server/pkg/update/update.go and focused tests; release/homebrew/update_tap.py, render_formula.py, promote_to_tap.py and their tests; relevant release scripts, .github/workflows/, README/docs, website configuration, and tap files. Locate exact current paths during execution rather than copying stale local versions. Tests: focused updater and Homebrew unit tests, owner-qualified endpoint assertions, workflow validation, documentation link checks, and website build/path checks.

- [x] Create the implementation worktree with git_worktree from reconciled current source; keep migration edits separate from unfinished user work.
- [ ] Search current tracked source for personal-owner API/download URLs, tap names, Pages URLs, plugin destinations, and absolute development paths; classify live references versus historical documentation.
- [x] Update updater/release endpoints and tests to cercano-ai while retaining redirect validation, token confinement, artifact checks, and existing release naming.
- [x] Update formula rendering/publishing and workflow destinations to cercano-ai/homebrew-tap; preserve checksums, optimistic locking, and authentication boundaries.
- [x] Inspect both tap repositories and compare formula history and current contents before selecting the canonical formula; stop if substantive differences require a decision.
- [x] Preserve and review Lattice cask references and behavior as part of the shared tap transfer.
- [ ] Prepare transition guidance for users of both personal taps and the dedicated homebrew-cercano repository; do not introduce duplicate independent publishers.
- [ ] Prepare legacy plugin sync owner changes and required token authorization without removing deprecated plugin support.
- [x] Prepare Pages links and deployment configuration using the current upstream website source; retain /Cercano when appropriate and do not add an unapproved domain migration.
- [ ] Update current installation and contribution documentation and badges; leave historic references alone where rewriting them would misrepresent history.
- [ ] Run focused tests and static checks; checkpoint reviewed source changes without publishing premature destinations.
- [ ] Record the exact commits and settings changes to apply during cutover, plus the fallback for each integration if its check fails.

## Phase 4 — Transfer GitHub repositories and restore integrations

Objective: move repository ownership and activate prepared integrations during a controlled window. Files: reviewed migration commits and execution evidence; GitHub settings and remote URLs change only after confirmation. Tests: before/after repository identity and metadata comparison, permissions checks, old/new endpoint probes, workflow configuration checks, and attachment access.

- [x] Present the transfer list, destination names, visibility/access findings, coordinated publication actions, and recovery limits; obtain explicit execution confirmation before transfers and publishing.
- [ ] Pause affected writers and automated publishing with approval, recording previous settings for restoration.
- [x] Transfer existing homebrew-tap, homebrew-cercano, cercano-claude, cercano-codex, cercano-gemini, Cercloud, and Cercano in the dependency-aware order established in Phase 2.
  - [ ] Verify each transfer completes and retains required history, refs, issues, pull requests, releases, visibility, and access before proceeding.
  - [ ] Stop immediately on an unexpected permission, naming, or transfer-policy result.
- [ ] Update each existing local repository's origin URL once per shared repository; preserve unrelated remotes and all local refs.
- [ ] Apply and publish the approved source, workflow, and documentation updates once their destinations exist; reconcile concurrent upstream changes without force-pushing.
- [-] Reauthorize or restore publishing credentials and environment settings only through approved secret-management paths.
- [ ] Publish the dedicated legacy tap's transition guidance without deleting its history or silently archiving it.
- [ ] Verify plugin synchronization targets and access with a non-destructive check; obtain confirmation before a test that overwrites generated plugin contents.
- [ ] Verify old GitHub repository and release URLs behave as required; do not recreate repositories at old names.
- [ ] Verify existing README attachment URLs download and render as video players after the issue-hosting repository transfer.
- [ ] Restore only validated automation; leave failing publishers paused and report them as blockers.

## Phase 5 — Validate distribution and GitHub Pages

Objective: demonstrate a working delivery path for new and existing users before moving local workspaces. Files: isolated test fixtures/environments, corrected migration source where necessary, and verification notes. Tests: canonical tap installation, existing-tap migration, updater compatibility, release asset integrity, Pages build/deployment, and link/media checks. No product release or destructive host installation changes without separate approval.

- [ ] Test canonical tap discovery and fresh Cercano installation in an isolated environment, avoiding modification of the user's active installation.
- [ ] Exercise an existing bryancostanich/tap installation's transition to cercano-ai/tap and verify upgrade behavior; also document/test the supported path from the dedicated tap where applicable.
- [ ] Verify Lattice cask availability and referenced artifacts remain intact under the moved shared tap.
- [ ] Test the updater's new API endpoint and the old endpoint used by already-released clients, including release metadata and representative artifact downloads.
- [ ] Verify signatures/checksums and release artifact naming have not been changed; use a dry run or nonpublishing gate for release automation.
- [ ] Report signing or publishing validation that cannot be completed without an actual release; request approval rather than publishing one as a test.
- [ ] Run the website's existing build checks and deploy through its Pages workflow.
- [ ] Verify https://cercano-ai.github.io/Cercano/, asset paths, internal navigation, installation links, and README media; document the old Pages address behavior explicitly rather than assuming it redirects.
- [ ] Confirm required integrations are operational and record any external/manual steps before proceeding to local relocation.

## Phase 6 — Relocate local repositories and worktrees

Objective: move local development into /Users/bryancostanich/git_repos/cercano-ai while preserving every original workspace and ensuring Git metadata remains valid. Files: local filesystem locations, Git worktree metadata and approved path-dependent configuration; no source cleanup. Tests: before/after manifest comparison, worktree/ref enumeration, content preservation checks, remote access, and representative developer commands.

- [ ] Produce an explicit old-to-new path map for all repository roots, worktrees, nested worktrees, and backup/document directories; retain a recognizable relative layout and detect collisions before moving.
- [ ] Include any new migration worktree created during this effort and prepare a stable execution location outside directories being moved.
- [ ] Obtain approval for the local pause window; stop affected writers and refresh the final preservation snapshot immediately before relocation.
- [ ] Recheck backup integrity and capture the recovery commands and metadata paths needed if relocation stops partway through.
- [ ] Relocate the three existing main repositories and their linked worktrees with supported Git move/repair procedures; account explicitly for nested worktrees and main-checkout move limitations.
- [ ] Move the standalone backup/document directories without filtering ignored files or deleting their originals before verification.
- [ ] Establish local checkouts for transferred GitHub-only repositories if still absent, keeping their destination names distinct from linked worktrees.
- [ ] Update approved editor/script/agent-session paths and other recorded consumers; preserve saved session data and list any paths needing manual user action.
- [ ] Verify every registered worktree exists, opens correctly, and retains its branch/HEAD and expected local changes.
- [ ] Reconcile dirty/untracked/ignored content and refs against the quiesced manifest; explain intentional migration commits separately from preserved user changes.
- [ ] Verify fetch/remote access and representative development commands from the new locations without starting full production builds unnecessarily.
- [ ] Resume approved local services and sessions from the new locations; retain backups and recovery references without unrequested cleanup.

## Phase 7 — Final audit and handoff

Objective: establish completion from evidence rather than successful transfers alone. Files: effort verification report and task statuses. Tests: final repository/worktree accounting, URL/reference audit, permission checks, and representative delivery smoke checks.

- [ ] Reconcile all seven remote repositories and every local source path against the inventory; account for newly created paths and commits.
- [ ] Confirm Cercloud privacy and approved access, canonical Homebrew ownership, single-source publishing, Lattice preservation, and legacy tap guidance.
- [ ] Audit remaining personal-owner references and old absolute paths; classify necessary historical/compatibility references instead of blindly replacing them.
- [ ] Verify release, plugin, Homebrew, and Pages automation states match the intended enabled/paused configuration and no temporary elevated permissions remain unintentionally.
- [ ] Provide the new repository URLs, local paths, Homebrew commands, Pages URL, evidence of preserved work, and backup/recovery locations.
- [ ] Identify any unverified external integration, missing credential, or manual action as an explicit blocker or limitation; do not mark required acceptance checks complete without evidence.
- [ ] Checkpoint final migration documentation and report the exact published commits; no archival or deletion of old backups/worktrees as part of completion.
