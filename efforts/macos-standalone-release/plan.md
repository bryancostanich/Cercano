# Standalone Cercano macOS Release

Implement the approved spec in spec.md: macOS arm64 only, colocated agent and terminal client, protected GitHub Actions signing and notarization, artifacts in the source repository's GitHub Releases, and a binary formula in bryancostanich/homebrew-tap. Preserve active agents and user data during upgrades. Do not install a background service automatically.

Execution approval covers local implementation and verification, not certificate export or upload, secret provisioning, repository visibility changes, pushing, public release creation, or tap promotion. Leave externally blocked tasks explicitly blocked rather than declaring the release ready. Delegate reconnaissance and mechanical implementation to scoped sub-agents. Use a feature worktree for implementation; preserve unrelated changes. Checkpoint solved units without pushing. Before a bug fix, reproduce the smallest failing case and confirm its root cause. Escalate newly discovered consequential design choices before implementing them.

## Phase 1 — Establish the installed release contract

Objective: resolve incomplete discovery before changing production behavior. Files: inspect the two module Makefiles and entry points, server scripts, CLI agentclient package, existing update and compatibility code, embedded asset declarations, and existing GitHub workflows; record evidence in efforts/macos-standalone-release/release-audit.md. Exact production files and test packages are identified by this audit, not guessed in advance. Tests: inventory existing coverage and specify small installation and upgrade probes.

- [x] Establish an isolated feature worktree and preserve the approved effort documents
- [x] Trace both installed entry points through symlink resolution, sibling executable discovery, agent startup, handshake, and restart guidance
- [x] Inventory required filesystem assets, embedded assets, optional integrations, runtime downloads, redistribution obligations, and minimum supported macOS version
- [x] Inspect current release workflows, repository release URLs, updater behavior, and tap conventions without modifying remote state
- [x] Identify the existing compatibility contract and tests; escalate any need for a new protocol contract before implementation
- [x] Record exact implementation targets, supported macOS floor, test commands, and release blockers in the audit

## Phase 2 — Make installed startup and upgrades safe

Objective: fix only demonstrated gaps in standalone installation and upgrade behavior. Files: entrypoint, agentclient, asset discovery, and update code identified in Phase 1; focused unit and integration tests beside those packages. Tests: temporary installation prefix with Homebrew-style symlinks, working directory outside the repository, already-running compatible and incompatible agents, and preserved state. Test subprocesses must use isolated configuration and sockets and must never terminate the developer's live agent.

- [x] Add installation probes for colocated binaries reached through Homebrew-style symlinks and prove any observed discovery or asset-loading failures
- [x] Correct demonstrated installed-path or required-asset gaps without introducing development-checkout dependencies
- [x] Add isolated integration coverage for a compatible already-running agent and incompatible connection behavior
- [x] Implement any missing actionable restart guidance under the established compatibility contract without automatic process termination
- [x] Verify Homebrew-managed installations are not overwritten by an incompatible self-update path; fix demonstrated conflicts
- [x] Verify configuration and conversation data are preserved across replacement and restart
- [x] Run affected package tests and installation/upgrade integration tests, then checkpoint the solved unit

## Phase 3 — Build strict release artifacts

Objective: produce version-matched arm64 binaries and complete archives with a strict release-only signing path. Files: release scripts under source/server/scripts or the existing release-script location established by the audit, related build targets, and script tests. Leave permissive local development signing intact. Tests: build-version checks, archive-content checks, architecture checks, and controlled signing/notarization failure cases without credentials.

- [x] Implement clean release builds for both binaries using one explicit tag-derived version and the audited macOS build requirements
- [x] Package required base assets and license notices with the binaries; exclude developer launchers, credentials, caches, and model weights
- [x] Implement fail-closed Developer ID signing with hardened runtime, secure timestamps, and signature verification of final executable bytes
- [ ] Implement notarization submission and acceptance verification using a supported submission container, followed by final archive checksums
- [ ] Add failure coverage for absent identity, signing failure, notarization rejection, and incomplete archive contents
- [ ] Verify archive architecture, matching version output, required assets, and executable permissions without asserting notarization success from mocked tests
- [ ] Checkpoint the artifact tooling and tests

## Phase 4 — Wire protected GitHub release automation

Objective: connect tested tooling to a protected, least-privilege release workflow. Files: .github/workflows release workflow, supporting release scripts, and operator documentation. Tests: workflow validation, shell checks, and controlled failure/rehearsal paths. Actual Apple service calls require operator-provisioned credentials and explicit authorization where applicable.

- [ ] Add a macOS arm64 release build and relevant test gate with explicit toolchain versions and narrowly scoped workflow permissions
- [ ] Restrict signing credentials to the protected release environment and prevent untrusted pull-request access
- [ ] Import the operator-provisioned signing identity into a temporary keychain and clean up credentials and temporary files on success or failure
- [ ] Gate artifact promotion on signatures, accepted notarization, archive validation, and checksum generation
- [ ] Support a non-public rehearsal that produces reviewable artifacts without public release or tap promotion
- [ ] Gate publication on explicit operator authorization and prevent silent replacement of existing versioned artifacts
- [ ] Document required environment configuration, certificate and notarization provisioning, rotation, and failure recovery without placing secrets in source or logs
- [ ] Validate the workflow and checkpoint the automation

## Phase 5 — Prepare the Homebrew formula and operator documentation

Objective: prepare a binary formula matching the tap's conventions and complete user-facing installation instructions without pushing. Files: a reviewable formula/template and promotion tooling in the release tooling location, plus installation/release documentation; the external tap is modified only in an authorized checkout. Tests: formula syntax/style, archive/checksum mapping, unsupported-platform rejection, and credential-free smoke tests.

- [ ] Prepare the macOS arm64 formula installing both binaries together from immutable GitHub Release URLs with SHA-256 verification
- [ ] Enforce the audited minimum macOS version and reject unsupported operating systems and architectures clearly
- [ ] Add a smoke test that needs neither model downloads nor credentials and verify version commands do not require network availability
- [ ] Prepare narrowly scoped tap-update automation that runs only after the referenced release artifacts are available and verified
- [ ] Ensure formula install and upgrade hooks neither stop agents nor register a background service
- [ ] Document tap installation, first-run model setup, explicit restart, PATH conflicts with the development launcher, troubleshooting, and uninstall data retention
- [ ] Run available formula checks, record externally blocked installation checks, and checkpoint formula tooling and documentation

## Phase 6 — Rehearse and validate on a clean Mac

Objective: establish actual distribution readiness rather than equating unit tests with successful notarized installation. Files: release verification record under efforts/macos-standalone-release and fixes to demonstrated failures. Tests: actual signed/notarized artifacts and the intended formula on a clean supported Mac, including trust and Keychain behavior. No public publication or credential provisioning is implied by plan approval.

- [ ] Obtain explicit operator authorization and provisioning for the protected release environment; verify Developer ID certificate validity without logging private material
- [ ] Run the non-public release rehearsal and record real signature and notarization results
- [ ] Verify clean-Mac downloaded-artifact trust behavior and formula installation without Go or a source checkout
- [ ] Verify first startup, agent connection, and a configured-model interaction; record any separately provisioned model or provider requirements
- [ ] Verify signed upgrade behavior with an active agent, preserved conversations/configuration, and Keychain access across versions
- [ ] Verify failure and recovery behavior, including unavailable release artifacts preventing tap promotion
- [ ] Record verification evidence and remaining limitations; resolve failures before recommending publication

## Phase 7 — Authorized publication and tap promotion

Objective: publish only after verified readiness and explicit user authorization. Files: release metadata and external tap formula as authorized; no repository visibility changes. Tests: unauthenticated artifact access, final checksums, formula installation from the published tap, and smoke test on the supported platform.

- [ ] Present readiness evidence and obtain explicit authorization for source push/tag, GitHub publication, and tap push before performing those operations
- [ ] Publish the approved immutable release artifacts, checksums, source revision, and release notes through the protected workflow
- [ ] Verify public artifact URLs and checksums before promoting the formula
- [ ] Promote the formula to bryancostanich/homebrew-tap using narrowly scoped credentials
- [ ] Verify installation and smoke tests from the published tap and record the shipped version
  ---

## Addendum — 2026-09-22: Compatibility clarification (user resolution)

Recorded during the Phase 1 audit. The spec above is unchanged and remains authoritative; this addendum documents a user decision that governs the compatibility work.

- The user explicitly rejected any new TUI/agent handshake or version gating.
- **Mixed-version client/server connections must be preserved; a version mismatch alone is never a blocker.**
- Audit finding: current code already satisfies this — `agentclient.Dial`/`connect` (`source/server/pkg/agentclient/client.go:88-137`) is a plain gRPC connection with no version handshake anywhere. No code or protocol changes are required or permitted for this.
- Consequently, the Phase 1 task "identify the existing compatibility contract and tests; escalate any need for a new protocol contract before implementation" is closed on the user's resolution; no escalation is needed. Evidence and remaining release blockers are consolidated in `release-audit.md`.

## Addendum — 2026-09-23: Automatic restart after upgrade (user resolution)

The user superseded the spec's "no automatic termination of a running agent" requirement. The governing contract is now:

- A **successful** upgrade — including a direct `brew upgrade cercano`, not only an update applied through Cercano — must restart an already-running agent so the new binaries take effect.
- A failed download or installation must leave the running agent alone.
- If no agent is running, upgrading must not start one.
- Still no version handshake or compatibility gating; a mixed-version connection is never a blocker.

The Phase 2 task "Implement any missing actionable restart guidance ... without automatic process termination" retains its original title for traceability, but was completed under this revised contract: the agent is now stopped and replaced automatically after a successful install, in addition to the user-initiated `/restart-agent` path. Implementation and its verified boundaries are recorded in `homebrew-restart-verification.md`; the Homebrew hook is activated by `release/homebrew/cercano.rb.in`, which is not yet published.
