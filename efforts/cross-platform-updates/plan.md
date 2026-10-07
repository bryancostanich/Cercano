# Cross-platform update experience

Execute the approved spec.md. The user approved the specification in conversation before this plan was written. All tasks start pending; prototype evidence is a baseline, not completed production implementation.

Use an isolated worktree from current main and checkpoint solved units. Do not modify the user's running installation or automatically publish artifacts. New design forks require the design-decision approval process; consequential details intentionally reserved by the specification are gates, not permission to choose silently. Phase order is dependency order; research/test preparation can overlap, but a later integration cannot claim success before its prerequisites pass.

Candidate new package paths below express responsibility boundaries, not a mandate to duplicate existing facilities. Confirm names and reuse opportunities against current main during Phase 1. Never import the diagnostic spike wholesale or land its branch-only workflow as a production workflow.

## Phase 1 — Establish the baseline and implementation boundaries

Objective: preserve the approved documents, audit current code, and establish a reproducible test baseline without disturbing existing work. Files: efforts/cross-platform-updates/, existing source/server/pkg/update/, source/server/internal/brewrestart/, source/server/pkg/agentclient/, source/server/internal/procx/, source/clients/cli/, source/proto/, and release/ inspected read-only initially. Tests: focused existing discovery, restart, process, UI and release-tool tests; establish native Windows/Linux/macOS runners with inert fixtures before modifying behavior.

- [x] Create an isolated feature worktree from current main and preserve the approved spec and plan
- [x] Trace existing notification surfaces, discovery cache, configuration, RPC conventions, drain lifecycle, subprocess ownership and installation lookup
- [x] Inventory installed layouts and package-manager ownership signals on all three platforms
- [x] Audit the successful Windows spike and extract reusable test fixtures without promoting prototype assumptions
- [x] Record baseline test results and exact source paths for subsequent phases
- [x] List unavailable native machines, credential stores, installer tools and publication resources as explicit blockers
- [x] Checkpoint the baseline and approved effort documents

## Phase 2 — Prove TUF integration and define security operations

Objective: validate a maintained Go TUF implementation and the required recovery behavior before depending on a feed. Files: isolated test module/fixtures under efforts/cross-platform-updates/tuf-proof/ initially; prospective source/server/internal/updatecoord/trust/ once the gate passes; release/update-feed/ for test-only publisher tooling. Tests: local HTTP repositories, generated ephemeral keys, injected clocks, corrupt targets, metadata expiry, rollback, interrupted refresh and sequential root rotation. No production keys or hosted production repository.

- [x] Inspect and pin a maintained Go TUF implementation and record license/dependency constraints
- [x] Demonstrate bootstrap from bundled trusted root metadata and verified platform-specific target download
- [x] Test tampered metadata and targets, wrong platform/architecture, rollback and freeze attempts, expired metadata and invalid local time
- [x] Test offline clients, sequential root rotation, interruption at every metadata-refresh boundary and safe failure without damaging the installed application
- [x] Demonstrate publication ordering and concurrent-publisher protection in an isolated test repository
- [x] Present the concrete key roles, thresholds, offline root custody, online signer permissions, expiry/renewal schedule and recovery procedure for operator approval
- [x] Present repository hosting and authentication choices for approval before provisioning anything external
- [x] Record the feasibility verdict and stop for review if library behavior contradicts the approved trust model
- [x] Checkpoint verified test tooling and security decisions without private keys

## Phase 3 — Define installation ownership and operation state

Objective: give every update a trustworthy installation identity and a single observable lifecycle. Files: source/server/pkg/update/, prospective source/server/internal/updatecoord/{installation,operation,policy}/, configuration types and tests. Tests: table-driven ownership detection, conflicting markers, symlinks, paths with spaces/Unicode, unknown/development installations, concurrent requests, persistence and crash-reload fixtures.

- [x] Define installation identity, owner/backend, scope, platform, architecture and release-source information using existing conventions
- [~] Corroborate provenance against executable paths and package-manager state; do not infer ownership merely from PATH
- [x] Represent unknown, ambiguous and development installations without permitting automatic file replacement
- [x] Define operation states, progress, errors, retry semantics and persisted recovery information
- [x] Separate announced releases from versions available through the installed package source
- [x] Record per-version dismissal, explicit self-update enrollment and managed-policy restrictions
- [x] Specify the Chocolatey opt-in ownership transition and uninstall/version-tracking contract before implementing it
- [x] Test operation deduplication, concurrent clients, unavailable backends and safe persistence/version migration
- [x] Checkpoint the ownership and operation foundation

## Phase 4 — Build shared lifecycle coordination

Objective: prevent update races and protect active work before implementing file activation. Files: source/server/internal/updatecoord/, source/server/internal/brewrestart/, source/server/pkg/agentclient/ launch and detach helpers, source/server/internal/procx/, source/server/internal/server/ drain handlers, and additive source/proto/ messages where needed. Tests: native process fixtures, multiple client subprocesses, abandoned locks, agent identity changes, drain cancellation and protocol compatibility. No tests against the live developer agent.

- [-] Define helper invocation and authenticated local control boundaries using existing transport patterns; request approval if an additional transport or privilege boundary is required
- [ ] Implement shared launch/update exclusion on Windows, Linux and macOS with stable lock identity and recovery after process death
- [ ] Identify the exact installation-owned agent, clients and relevant child processes without broad name matching or rename-probe ownership assumptions
- [ ] Implement the wait-for-idle default and a visible admission barrier once draining begins
- [ ] Add explicit user-confirmed cancellation of active work without an implicit timeout-to-kill policy
- [ ] Restore normal admission on deferral, safe pre-activation cancellation or failed preparation
- [ ] Coordinate restart and reconnect without introducing a version compatibility handshake
- [ ] Define health success for expected agent version/readiness and connected clients, including a bounded no-client or previously-absent-agent probe
- [ ] Preserve the invariant that an upgrade does not leave a previously absent agent running
- [ ] Port existing Homebrew behavior onto the shared foundation while retaining its ownership safeguards
- [ ] Test race conditions, concurrent launch, process identity replacement, non-draining work and platform child-process cleanup
- [ ] Checkpoint lifecycle coordination after native gates pass

## Phase 5 — Implement self-managed staging and activation

Objective: turn the versioned-directory experiment into a recoverable installation operation, starting with per-user Windows and then direct Linux. Files: prospective source/server/cmd/cercano-update/ or an approved equivalent helper boundary; source/server/internal/updatecoord/{download,staging,activation,recovery}/; stable launcher entrypoints and fixture binaries. Tests: real locally built binaries, local signed TUF repositories, native filesystem/process probes, fault injection at each durable transition. Code-location and launcher/helper bootstrap details must be reviewed before introducing a new executable contract.

- [ ] Define and approve the stable launcher/helper bootstrap and self-upgrade contract, including who can modify selection and journal files
- [ ] Implement bounded verified target acquisition through TUF and strict archive validation before staging
- [ ] Reject traversal, symlinks/reparse surprises, duplicate or unexpected members, missing binaries, oversized downloads and wrong platform/architecture
- [ ] Stage agent and client in one complete immutable version directory with safe permissions
- [ ] Implement durable operation records, version selection and startup reconciliation with platform-specific semantics
- [ ] Activate only after the lifecycle coordinator has reached the safe boundary
- [ ] Test interruption before staging, before selection, during selection and before health confirmation on native Windows and Linux
- [ ] Implement explicit recovery to the previous complete version after initial startup or health failure
- [ ] Verify both executables and real protocol readiness rather than trusting installer exit status
- [ ] Delete the prior version after successful health verification; record and retry locked-file cleanup without force-killing users' processes
- [ ] Protect the active version, live stages and user data from cleanup; safely collect abandoned stages
- [ ] Implement explicit enrollment/migration for eligible manually extracted installations without silently relocating existing ZIP/tarball users
- [ ] Test insufficient disk space, permissions, antivirus/locking interference, long/Unicode paths and repeated recovery attempts
- [ ] Checkpoint the Windows and Linux self-managed engines separately with precise native evidence

## Phase 6 — Add the consistent in-app update experience

Objective: expose the same discovery-to-completion experience while dispatching to the correct backend. Files: source/server/pkg/update/, additive source/proto/ update messages/generated bindings, server handlers, source/clients/cli/internal/ui/ and command handling, configuration documentation. Tests: deterministic UI/state tests with fake backends and integration tests over the real client/server interface, including older peers.

- [ ] Extend bounded cached discovery with explicit fresh checks, backoff and distinct offline/error states
- [ ] Keep version/help commands offline and ensure interactive startup never waits on update discovery
- [ ] Add Update, Release notes and Later with per-version dismissal and managed-policy handling
- [ ] Show installability separately from release announcements when package repositories lag
- [ ] Present installation owner, target version, privilege needs and active-work consequences before applying
- [ ] Connect user-approved actions to the operation service without embedding release-provided shell commands
- [ ] Stream progress and waiting/draining/restarting/cleanup states consistently to all attached clients
- [ ] Handle lost/reconnected clients and recover operation state after UI restart
- [ ] Provide exact safe command guidance when automatic elevation or backend execution is unavailable
- [ ] Display verified success, recovered failure and deferred cleanup separately
- [ ] Test cancellation and dismissal without quietly enabling unattended updates or changing channels
- [ ] Verify graceful behavior with older agents/clients without blocking ordinary connections on version differences
- [ ] Checkpoint UI and interface changes after integration tests pass

## Phase 7 — Integrate package-manager backends

Objective: initiate manager-owned updates without taking ownership of their files or privileges. Files: source/server/internal/updatecoord/backends/, existing brewrestart compatibility entrypoint, release/homebrew/, prospective release/debian/ and release/chocolatey/, native package fixtures and operator docs. Tests: mocked command construction plus disposable installed-system tests for each manager; mocks alone do not satisfy completion.

- [ ] Implement Homebrew availability/version checks and targeted upgrade invocation without sudo
- [ ] Coordinate app-initiated and external Homebrew upgrades without double-drain/deadlock between the app and post-install hook
- [ ] Test package-upgrade success separately from restart failure; never independently roll back Homebrew files or links
- [ ] Define and approve APT privilege handoff and user-scoped coordination before privileged operations
- [ ] Implement APT source/version detection and targeted update initiation or safe interactive command fallback
- [ ] Ensure root-run package scripts neither launch a root agent nor blindly stop other users' agents
- [ ] Implement Chocolatey version/scope detection, appropriate authorization prompts and wrong-account/SYSTEM rejection for per-user installation
- [ ] Ensure Chocolatey upgrade scripts cannot downgrade a newer explicitly self-updated installation
- [ ] Implement the approved explicit transition to per-user self-management where allowed, with managed policy able to refuse it
- [ ] Test package-manager failure, repository lag, missing manager, privilege denial and external upgrades while multiple clients are connected
- [ ] Checkpoint each backend only after its ownership and process-isolation tests pass

## Phase 8 — Package and rehearse distribution

Objective: supply real installation paths and verified update feeds without silently publishing production changes. Files: release/update-feed/, release/debian/, release/chocolatey/, installer/launcher packaging as approved, .github/workflows/ release jobs, existing archive build/verification scripts, and installation/operator docs. Tests: fixture-signed repositories and disposable machines/VMs, deterministic metadata verification, packaging checks and staged non-publishing CI.

- [ ] Define initial Windows installer format and signing/provisioning requirements for approval before adding production signing credentials
- [ ] Package per-user Windows installation, stable entrypoints and uninstaller with preserved data and recorded provenance
- [ ] Package Linux direct-install enrollment and a Debian package without conflating their ownership
- [ ] Build a signed test APT repository and test install/update/uninstall and key-rotation guidance
- [ ] Build a test Chocolatey package around the approved installer and exercise actual version reconciliation
- [ ] Integrate signed TUF metadata generation, artifact verification and ordered publication using test keys first
- [ ] Retain macOS signing/notarization and manager-specific verification; do not treat TUF as an Authenticode substitute
- [ ] Confirm release metadata and package-manager availability cannot advertise incomplete or older releases over newer state
- [ ] Obtain explicit approval for production key custody, secrets, endpoints, repository registration and publication operations
- [ ] Run non-publishing CI rehearsals for every supported platform and installation backend
- [ ] Document renewal, root rotation, expired-metadata recovery, failed publication recovery and rollback boundaries
- [ ] Checkpoint packaging and release tooling; mark external-resource blocks honestly rather than treating them as passed

## Phase 9 — Installed-system acceptance and rollout gate

Objective: prove the user journey and failure recovery before recommending the feature. Files: integration fixtures, native CI/VM automation, efforts/cross-platform-updates/verification.md, user and operator documentation. Tests: clean installed environments, full protocol/model-operation checks where required, controlled faults and an authorized small release rehearsal.

- [ ] Exercise notification, release notes, dismissal and Update through completion on each supported installation backend
- [ ] Verify a real agent task drains without loss, new work is visibly deferred, and explicit cancellation is distinct from automatic interruption
- [ ] Verify configuration, conversation history and credentials survive install/update/recovery/uninstall policies
- [ ] Exercise multiple clients and installations without cross-user or cross-installation interference
- [ ] Prove Windows per-user behavior under normal launch, same-user elevation and refused alternate-account/SYSTEM contexts
- [ ] Prove Linux direct and APT-owned paths on desktop and headless systems with real privilege boundaries
- [ ] Prove Homebrew behavior on a machine without the development checkout and confirm no forced file ownership changes
- [ ] Execute the native crash/recovery matrix and confirm old-version deletion only after verified success
- [ ] Exercise TUF expiry, rotation, revoked/incorrect keys and offline recovery without a verification bypass
- [ ] Document known limitations and keep deferred machine-wide Windows/direct-macOS self-updates clearly out of the supported feature set
- [ ] Obtain final review of security boundaries, native evidence and operator recovery procedures
- [ ] Request separate authorization for production release/enrollment; never update the developer's installation as a test
- [ ] Checkpoint final verification and mark completion only for actually passed acceptance criteria
