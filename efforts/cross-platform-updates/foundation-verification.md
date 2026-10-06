# Phase 3 foundation verification — partial implementation

New pure packages under `source/server/internal/updatecoord/installation` and
`operation` are not called by production entrypoints. They do not touch user
files, spawn processes, run package managers, request credentials or contact a
feed. Phase 3 as a whole is not yet complete.

## Ownership value model

Represents unknown/development/package-manager/self-managed ownership, scope,
platform/architecture, root/executable and release source. Conflicting claims
fail closed. Manager presence or an installed package does not prove it owns
the running executable. A manager-owned symlink alone does not prove ownership
of an arbitrarily retargeted destination. Self-enrollment must bind to the exact
observed executable and installation root; a global boolean is insufficient.

Regression tests first demonstrated unbound enrollment, link-only ownership and
relative executable paths being accepted; all now refuse those cases. Target-OS
path validation runs independently of the host OS. This does not resolve live
symlinks or normalize Windows filesystem case: real platform probes must supply
canonical observations. Observational evidence is not a capability authorizing
privileged writes. Existing production `DetectInstallMethod` is unchanged.

Verified availability is distinct from an upstream announcement, and
`InstallableFor` checks the installation's source and actionable classification.
Actual package/TUF availability probes and freshness/version policy are pending.

## Operation value model

The mutex-protected in-memory store deduplicates same-target requests and rejects
conflicting concurrent targets for an installation. Every event includes the
current operation ID; old or omitted IDs cannot mutate a replacement operation.
Consent to cancel work does not imply that work finished. Explicit idle/drained
callbacks are required before the install transition. Cancellation during
activation requests recovery rather than pretending installation stopped.
A failed protected operation blocks a replacement operation until a safe backend
outcome is explicitly acknowledged. Health failure has an explicit reason,
recovery is distinct from completion, and old-version cleanup only follows
health verification. Deferred cleanup is explicit rather than reported as done.

All callback evidence remains trusted caller input. No authentication, real
admission barrier, process lock, filesystem transaction, installer, or watchdog
is implemented by this package. User-facing reason strings must be sanitized by
callers: the type itself cannot guarantee they contain no secrets.

A JSON record schema/round-trip is defined, but no durable record is written or
restored. In-memory IDs are store-local; persistence must preserve uniqueness and
reject stale events across restart before any backend can use it. Per-event
replay semantics, storage migration and actual idle/process probes remain work.

## Checks

- `go test -race ./internal/updatecoord/... ./pkg/update` — pass.
- `go vet ./internal/updatecoord/...` — pass.
- Existing `internal/brewrestart` and `pkg/agentclient` tests — pass.
- Delegated whole-server `go build ./...` — reported pass; no Windows/native
  installation acceptance is inferred from that compilation.

## Consent, dismissal and Chocolatey delegation records

The approved registration-retention contract is represented as explicit delegated
update ownership; the uninstall owner remains Chocolatey. Current manager-file
ownership, canonical root/executable containment, user scope, observed admin
policy, supported installer contract, install identity, channel and stable feed
identity must corroborate the record. Merely loading consent JSON is not runtime
authority. Machine-wide, unmanaged-unknown, stale, revoked, unsupported or
conflicting evidence remains refused. Package record drift is informational:
no function edits Chocolatey's database or authorizes file replacement from
version comparison alone.

Per-version dismissal is bound to installation/source/channel/version, not a
global notification suppression switch. Records validate schemas, canonical JSON
field names, duplicate keys, bounded size, valid UTF-8 and strict fields. Unknown
nonzero schemas cannot be silently re-encoded as the current schema. Stable
version comparisons reject invalid or overflowing versions; prereleases remain
outside the current stable release pipeline contract rather than being guessed.

Regression probes caught and fixed previously accepted unknown schemas,
unobserved admin policy, absent channel/feed binding, out-of-root executables,
invalid version comparisons, case-insensitive JSON key aliases, invalid UTF-8
replacement and oversized records. Targeted race tests for all three updatecoord
packages plus pkg/update, and updatecoord vet, pass. Serialization is memory-only;
no preference or enrollment file is actually persisted yet.

A delegate also ran an unrequested wider server race suite and reported a race
in internal/engine/ollama. It has not been reproduced against the base commit;
therefore it is not classified here as pre-existing, fixed or caused by this
change. The appropriately scoped suites above pass. No unrelated fix was made.

No runtime update behavior, configuration format, package installation or
release feed was changed. No code pushed or merged into main.
