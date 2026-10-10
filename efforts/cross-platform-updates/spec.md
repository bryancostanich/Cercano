# Cross-platform update experience

## Status and purpose

Approved by the user in conversation. The design choices and specification are the implementation anchor. The execution plan has a separate approval gate; specification approval alone does not authorize implementation or release publication.

Cercano needs one understandable update experience across Windows, Linux, and macOS: tell users an update is available, explain what will happen, let them initiate it, report progress, and confirm the running application has actually updated. Installation ownership must remain clear, and active agent work must not be interrupted silently.

The aim is not to recreate every package manager. Shared Go coordination handles Cercano's processes and user experience. Homebrew, APT, and Chocolatey continue to manage installations they own. A Go-native helper handles whole-package activation for explicitly self-managed installations. Velopack is excluded by user decision.

## Current evidence and gaps

`source/server/pkg/update/update.go` already provides GitHub release discovery, version comparison, a 24-hour cache, and a three-second HTTP timeout. Its installation model only distinguishes Homebrew from manual installations, and its suggested action is either `brew upgrade cercano` or a download link. This is useful groundwork, not a complete update service or a trusted self-update feed.

`source/server/internal/brewrestart/` implements an ownership-checked Homebrew restart path; `coordinator_unsupported.go` rejects other operating systems. The existing non-Unix client launch-lock implementation in `source/server/pkg/agentclient/launch_lock_other.go` is a no-op. These must not be mistaken for a cross-platform lifecycle coordinator.

The `spike/windows-go-updater` branch contains an isolated module under `spikes/windows-go-updater`. Native Windows run 37070213042 passed against commit 8c100fdc2d1744063fbd9eb119cd79ec18e8c06a. Local race tests passed as well. The experiment demonstrated real library release discovery, explicit checksum validation, staging two fixture binaries, version selection, lock contention/reacquisition, and explicit rollback after an injected health failure.

That spike is not production code. `creativeprojects/go-selfupdate` v1.6.0 replaces one executable at a time; two calls do not create a package transaction. Its archive extraction needed additional structural checks. Its SHA-256 sidecar validator provides integrity, not an independent publisher-authentication boundary. The held-open Windows fixture did not prove that rename probes reliably detect all running applications. Automatic restart supervision and crash/power-loss durability remain unproven.

Native CI already builds Windows and Linux archives, but full runtime/installation validation remains separate. The Homebrew formula is live. A signed APT repository and production Chocolatey installer package are not assumed to exist; those are prerequisites for their respective update backends, not capabilities that can be claimed from archive smoke tests.

## Goals and completion definition

All supported installation paths expose the same in-app concepts: update availability, release notes, Update, Later, progress, failure recovery, and a verified completion result. A user does not need to remember the original installation method merely to find an update action.

Discovery distinguishes a newer release announcement from an installable update for the current platform, architecture, channel, installation scope, and package source. For example, a GitHub release that has not reached the Homebrew tap or APT repository must not be presented as a package-manager update ready to install.

One shared Go coordination model handles operation ownership, process draining, concurrent clients, cancellation, restart verification, and cleanup. Platform-specific primitives and installation-specific backends remain explicit rather than being hidden behind assumptions that only hold on macOS.

Self-managed updates deliver a consistent agent/client pair through a verified complete package. They support recovery from interrupted activation and failed initial health verification. Package-manager installations use their manager to change files and do not bypass its database, permissions, or administrative policy.

The implementation is complete only after native installed-system tests exercise real operations on Windows, Linux, and macOS. Compiling an artifact or reporting `--version` is insufficient evidence of update safety.

## Approved simplification: one-shot utility, not a service

The user explicitly approved a short-lived Go update utility in place of the
proposed interactive updater service. This revises the execution brief: no new
listening control API, updater daemon, socket/pipe service or control credential
system. Existing app/agent coordination handles consent and safe preparation;
a narrowly scoped utility performs one operation independently of agent lifetime,
uses installation-wide exclusion, and records progress/outcome durably. Inherited
output is best-effort and must not make the operation depend on an attached UI.

Do not add privileged update triggers to unauthenticated existing RPC merely to
avoid the rejected service. Prefer direct local launch from the initiating app;
validate operation identity, installation policy and inputs before side effects.
Do not hold a parent-owned lock that the child then waits on. A disconnected UI
is not consent to cancel work or kill an installer. Ordinary version compatibility
and connection behavior remain unchanged. Installer/bootstrap and real privilege
handoff still retain their existing review gates.

## User experience

During normal interactive use, checks run asynchronously with bounded requests, caching, backoff, and a clear distinction between no update and inability to check. Checking must not delay startup, invoke elevation, launch a model, or block ordinary work. Version/help commands remain offline and side-effect-free.

The notification names the available version and presents Update, Release notes, and Later. It respects a per-version dismissal and managed-environment policy. An explicit Check for updates action permits a user to request a fresh bounded check. No check silently opts a user into unattended installation or a different release channel.

Before applying, show the installation owner, versions involved, required permission prompts, and any active work that must finish. When privileged execution cannot be initiated safely in the current session, give the exact appropriate command and explain how the app will recognize completion. Never collect or store the user's administrator or sudo password.

Observable operation states include checking, update announced but unavailable through the installed source, ready to download, downloading/verifying, waiting for work, draining, installing/activating, restarting, verifying health, cleaning up, complete, deferred, cancelled, and failed/recovered. These are user-facing semantics; the exact internal state schema belongs to implementation design and must preserve their distinctions.

Multiple attached clients observe one installation's update operation rather than starting competing updates. Version differences alone never become a connection gate: preserve the prior decision against a mandatory version-compatibility handshake. Version reporting used to verify update completion is informational and operation-specific.

## Installation ownership and platform behavior

Ownership is determined from trustworthy installation records and actual executable paths, corroborated with package-manager state where applicable. A command merely appearing on PATH is not enough. Conflicting or unknown provenance fails safely to guidance or an explicit enrollment flow, not guessed file replacement. Development checkouts are never silently converted or modified.

### Homebrew-managed macOS

Homebrew owns downloading, package verification, Cellar installation, links, and package metadata. The app offers to run the relevant upgrade operation without sudo, or provides the exact command when execution is unavailable. The shared lifecycle coordinator subsumes the application-specific behavior currently provided by `restart-after-upgrade`.

The updater never edits Homebrew-managed binaries, switches Homebrew links itself, or silently rolls back package files after a health failure. It reports whether installation succeeded separately from whether the agent restarted successfully. No agent is launched merely because a package was upgraded when none had been running.

### APT-managed Linux

APT owns package downloads, repository verification, installation, and package metadata. Packaging must not launch a root-owned Cercano agent or blindly stop agents belonging to other users. User-scoped coordination is separate from the privileged package transaction.

The in-app action uses an explicit system authorization mechanism or gives an actionable command. The initial release must not invent an always-privileged background service merely to avoid a prompt. Installing the required `.deb` package and signed repository is part of making this path genuinely available; repository hosting and credential provisioning require explicit approval before publication.

### Chocolatey-managed Windows

Chocolatey owns installation and upgrades by default. Its package and the app must agree on installed scope and identity. Contexts such as elevation under another administrator or execution as SYSTEM must not silently install into the wrong user's profile.

Users may explicitly opt into self-managed updates where supported for per-user installations. That is an ownership transition with recorded consent and a documented uninstall/version-tracking contract, not a second updater overwriting package-managed files concurrently. Chocolatey Open Source does not automatically track self-updated application versions. Package scripts must detect the actual installed application version and never downgrade a newer installation to reconcile package metadata. Managed policy may prohibit this opt-in.

### Self-managed installations

Initial Windows self-update support is per-user. Machine-wide installations remain package-manager/admin-controlled; a privileged self-updater is out of scope for the first milestone.

Linux direct installations use the same verified package/coordinator contract with Linux-specific primitives and explicit enrollment. A manually extracted archive is not automatically an enrolled installation. Direct-install macOS self-updates may follow after Windows and Linux; Homebrew remains the recommended macOS path and is included in the initial notification/action experience.

The default self-managed layout uses versioned directories and stable public commands, `cercano` and `cercano-cli`. A trusted helper runs from a location that is not being removed or overwritten by the operation it coordinates. Updating the helper and stable launchers themselves requires a defined bootstrap/recovery contract; the spike does not solve this implicitly.

## Active work, restart, and cleanup invariants

Wait for idle by default. Downloads and verification may proceed while work continues, but activation must not silently cancel work. The user can defer the update or explicitly request cancellation of active work after a clear warning. Unattended deadline-based forced cancellation is not part of the initial interactive policy.

After the coordinator commits to draining, reject or defer new work with an observable reason and prevent a second client from launching an old agent. Already-running work may finish. Cancelling a pending update before activation restores normal admission and releases coordination resources. Cancelling during a package transaction or activation must follow safe backend recovery rather than abruptly killing an installer.

Identify and coordinate only the processes belonging to the target installation and user. Account for attached clients and child model-runtime processes holding versioned files. Do not use broad process-name killing or a rename probe as authoritative process ownership evidence.

For self-managed activation, preserve the previous version until health verification succeeds. Health includes the intended agent reporting the expected release, being ready for work, and attached clients reconnecting successfully. With no attached client, use a bounded protocol-level health check. With no agent previously running, use a bounded isolated health probe if required and leave no new background agent running after completion. Define handling for clients that were already disconnected; an absent client must not hold cleanup indefinitely.

If initial startup or health verification fails, restore the previous complete version selection and report recovery. Do not retain a permanent rollback cache after successful verification. Delete the superseded version once verified healthy and no process still depends on it. If locked files prevent deletion, record pending cleanup, retry safely later, and show cleanup as pending rather than claiming deletion or force-killing the owner. Never delete the selected version, an active staging operation, or user data.

The user approved extending the existing SQLite updater store for authoritative activation intent and recovery facts. Use additive, validated schema migrations that preserve existing operations and records; do not introduce a separate journal file. A small launcher-readable selection file remains separately reconciled against that journal. Database commits and filesystem selection changes are not one atomic operation. Migration approval covers implementation and isolated verification, not modifications to live installations.

The user approved restoring explicit prior absence after a failed first activation: remove only the exact selection created by that operation, under installation exclusion and after proving the owned candidate has stopped. Do not delete version directories or user data. Record distinct absence-restoration intent and completion; mismatches or uncertain process ownership must refuse automatic removal.

Power loss and process termination must leave a recoverable state. A selection file alone is not proof of a crash-safe transaction on Windows. Define durable journal/selection behavior, single-writer exclusion, startup reconciliation, and cleanup of abandoned stages, then verify them with fault injection and native tests.

## Trust model

TUF (The Update Framework) is the approved authentication model for self-managed updates across Windows, Linux, and macOS. Use a maintained Go implementation rather than implementing the protocol from scratch. The early integration gate must verify compatibility, metadata publication, expiry behavior, sequential root rotation, rollback/freeze protection, target selection, and recovery from interrupted metadata refresh.

Clients bootstrap from trusted root metadata shipped with the application. Release selection, size/hash verification, and download limits precede staging and execution. A GitHub release or matching unsigned checksum is not sufficient authorization. Integrity failures, expired required metadata, wrong platform/architecture, unexpected archives, or a downgrade attempt fail closed for the update while leaving the installed application usable. Do not bypass TUF because the machine is offline or its clock is wrong; explain the problem and recovery path.

TUF does not replace platform signing. Keep Apple Developer ID signing and notarization. Windows Authenticode remains a separate distribution/signing requirement to resolve before claiming a trusted signed Windows installer. APT repository signatures and package-manager verification remain separate for their owned installations.

Private root keys must not be embedded in the app or placed in ordinary release CI. Public keys/root metadata ship to clients. Exact TUF roles, thresholds, signing identities, secret storage, online signer permissions, repository hosting, expiration periods, and emergency recovery procedures are security-sensitive implementation gates requiring explicit operator sign-off before provisioning. They are not silently chosen by approving this specification. The feasibility work uses test keys and isolated repositories only.

Update repositories must publish metadata and target artifacts in an order that never advertises incomplete releases. Bootstrap and key-rotation procedures must account for offline clients. Prevent a concurrent older publish from replacing newer feed state. Existing published GitHub tags remain immutable unless a separate explicit exception is authorized.

## Approved decisions

### Per-user Windows self-updates first

The user approved per-user self-updating installations as the first milestone. Machine-wide installations remain managed by their package manager/administrator.

| Axis | Per-user self-updates first — chosen | Per-user and machine-wide self-updates immediately |
|---|---|---|
| Scope | One self-managed user scope | Two scopes including privileged activation |
| Cost | User-scoped ownership and wrong-account rejection | Additional elevated helper and multi-user coordination |
| Risk | Must reject unintended execution contexts clearly | Also risks cross-user interruption and privilege mistakes |
| Outcome | Personal-use path first, managed updates preserved | Shared-machine self-updates available immediately |
| Side effects | Machine-wide self-updater deferred | Broader security and recovery test matrix now |

### Versioned directories with stable launchers and bounded retention

The user approved versioned directories, conditional on deleting old versions after successful launch. This specification makes successful launch a readiness/reconnection check and defers deletion only for live references or cleanup failures.

| Axis | Versioned directories — chosen | In-place replacement with backups |
|---|---|---|
| Layout | Complete releases plus one selection point | Existing paths replaced one binary at a time |
| Cost | Stable launchers, selection recovery, cleanup | Replacement journal, backups, partial-restoration logic |
| Risk | Selection/durability and launcher compatibility must be proven | Mixed versions possible during interrupted replacement |
| Recovery | Restore prior complete selection before success | Restore both backups and repair partial replacement |
| Side effects | Temporary double disk use; remove old version after health | Less layout migration; more intermediate file states |

### TUF for self-managed update authenticity

The user explicitly chose TUF after reviewing its cross-platform role and separation from platform code signing.

| Axis | TUF — chosen | Custom signed update manifest |
|---|---|---|
| Cost | Integrate client and metadata publication roles | Implement manifest and trust/lifecycle rules |
| Risk | Operational expiry/key-management errors can block updates | Custom rollback, freeze, and rotation errors can undermine trust |
| Outcome | Established authentication and metadata lifecycle protocol | Simpler initial metadata format |
| Side effects | Additional signing operations and recovery procedures | More security-protocol ownership inside Cercano |

### Wait for idle, explicit cancellation only

The user approved waiting for idle and never cancelling active work without explicit consent.

| Axis | Wait for idle — chosen | Deadline then automatic cancellation |
|---|---|---|
| Cost | Pending operation, admission barrier, explicit cancellation UI | Same plus deadline policy and forced-cancellation behavior |
| Risk | Stuck work can postpone activation | Work or external operations can be interrupted unexpectedly |
| Outcome | Preserves user work unless the user chooses otherwise | Predictable unattended completion window |
| Side effects | Needs clear waiting/defer controls | Needs prominent countdown and stronger cancellation guarantees |

Shared Go coordination, exclusion of Velopack, package-manager ownership by default, and an in-app update action were settled before these four decisions. They are constraints, not questions to reopen during routine implementation.

## Validation and rollout requirements

Tests must separate pure state/ownership/trust logic from platform integration. Use temporary installations, fixture binaries, local TUF repositories, and test keys. Never exercise tests against the developer's live agent, credentials, Homebrew installation, or production update repository.

Native Windows tests must cover normal per-user installation, same-user elevation, alternate-account/SYSTEM rejection, locked files, process-tree handling, paths with spaces/Unicode, concurrent clients, staged helper execution, and interrupted activation. Native Linux tests must cover direct and APT-owned installations, non-root user operation, privilege boundaries, headless sessions, and update/reconnect behavior. Native macOS tests must cover actual Homebrew-style installation and ownership-aware restart with signed fixtures when separately authorized.

Fault injection covers network failures, unavailable package versions, corrupt or malicious archives, signature/hash failures, expired and rotated metadata, insufficient disk space, permissions, unavailable elevation, active work, cancellation, helper crashes, machine interruption, health failure, and cleanup that must be retried. Repeated update/recovery attempts must be idempotent and must not create multiple active installations or agents.

End-to-end acceptance covers the notification-to-action journey, installation-owner-specific progress, preserved configuration/conversations/credentials, actual running-version verification, failed activation recovery, and cleanup after success. No manager command exit code alone proves the agent updated. No health success claim is inferred from compilation alone.

Rollout starts with isolated TUF and Windows lifecycle proofs, then shared operation/notification behavior and installation backends, then packaging and native installed-system rehearsals. This is sequencing intent, not the execution checklist. The plan will specify concrete phases, files, tests, and approval gates after this spec is approved.

## Non-goals and authorization boundaries

No Velopack integration, silent package-manager ownership takeover, automatic root-owned agents, broad process termination, default forced cancellation, permanent old-version cache, or mandatory client/agent compatibility handshake.

No promise of machine-wide Windows self-updates or first-milestone direct-install macOS self-updates. Supporting APT does not imply support for every Linux package manager or distribution.

Spec/plan approval does not authorize exporting signing keys, changing GitHub secrets or repository protections, publishing package repositories, changing existing release tags, enrolling the developer's installation, or releasing new production artifacts. Those operations require explicit approval at their implementation gates. Existing workflows and published releases remain undisturbed during the prototype and testing phases.
