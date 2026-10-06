# SQLite updater-state slice — verified locally, native CI pending

The user approved a separate per-user/per-installation SQLite database outside
all versioned binaries, shared across versions, and separate from conversations.
`source/server/internal/updatecoord/state` is the first storage primitive. It is
not wired into startup, the UI, enrollment, package scripts or activation.

## Implemented

- Pure target-platform state-path selection with no environment reads: Windows
  LocalAppData, Linux XDG_STATE_HOME or ~/.local/state, macOS Application Support.
  Opaque IDs have a canonical lowercase ASCII component representation to avoid
  platform aliases; paths reject traversal and Windows device-path forms.
- Explicit `Open(root, installID)` only. Tests use temporary roots. Caller must
  establish a trusted existing per-user base; the package does not create or
  chmod arbitrary parent directories or choose a live home automatically.
- New managed directories are private on Unix and databases are exclusively
  created with mode0600. Unsafe writable managed directories, public database
  modes, symlink/nonregular database files and SQLite sidecars are refused.
- Application ID, initial schema version1, exact recognized schema objects and
  installation binding are checked. Existing databases receive a read-only
  preflight before a writable connection. Foreign/future/corrupt/empty-truncated
  state is refused without automatic reset. Initial creation is transactional;
  an interrupted incomplete initial file is left for explicit diagnosis rather
  than deleted or silently treated as an empty installation.
- Write-ahead logging with FULL synchronous mode, bounded busy waits/context-aware
  retries and a dedicated connection held for each entire write transaction.
  Rollback failure discards the uncertain connection rather than pooling it.
- Persistent monotonic operation IDs and typed operation/delegation records with
  revision compare-and-swap. Unallocated IDs and stale revisions cannot overwrite
  records. Counter overflow is refused. Policy records use the strict policy
  decoder and installation binding. Operation rows reject unknown/ambiguous JSON,
  invalid UTF-8, unsupported schemas/states and malformed timestamps/identity.

## Actual regression evidence

The interrupted delegated implementation compiled but lacked tests. Added
probes reproduced interleaved transactions on one pooled connection, 0644 files,
silent reinitialization of an existing empty database, invalid path acceptance,
unknown schema objects, unallocated IDs and ambiguous JSON. The fixes pass the
same probes. A blocked-writer cancellation probe initially took approximately
five seconds despite a 50ms context; short SQLite busy waits with bounded retry
now pass the under-one-second cancellation assertion.

Two real child processes allocate distinct persistent IDs. A separate test kills
an owned child during an uncommitted transaction and reopens the database: the
uncommitted counter change disappears and a committed ID is not reused. Children
operate only in parent TempDirs and are killed/reaped by their parent tests.
These are process-interruption tests, not proof of hardware power-loss behavior.

A policy version-boundary test also caught unsigned64 inputs accepted by the
signed-native-integer comparison helper and noncanonical leading zeros; those
inputs now fail validation before comparison.

## Verification performed

- Race tests: all updatecoord packages (installation, operation, policy, state)
  and existing pkg/update — pass on native macOS arm64.
- `go vet ./internal/updatecoord/...` — pass.
- Existing brewrestart and agentclient tests — pass.
- All four updatecoord test packages compile for Windows/Linux x64 with CGO off.
  These binaries were not executed on Windows/Linux in this turn.
- State package has twenty top-level test functions; its helper-only worker is
  skipped in the parent invocation and exercised by subprocess parent tests.
- Branch-only three-platform workflow YAML checked for read-only permission,
  no signing environment/secrets, no publication and exact feature-branch scope.
  Workflow has not been pushed or run.

## Remaining limitations and next slice

- Native Windows/Linux execution is a gate before relying on platform SQLite
  locking, URI paths and interruption behavior. CI requires an explicitly
  authorized feature-branch push; no publication or signing is required.
- Unix mode bits are NOT Windows ACL enforcement. Windows ACL/reparse ownership
  checks and native tests remain required before real Windows enrollment.
- Path checks are preflight checks, not descriptor-anchored protection against a
  same-user concurrent filesystem replacement. Caller identity/privilege and
  supported local-filesystem validation remain required. Network-filesystem
  compatibility is not established by these tests.
- StatePath supplies syntax/layout, not proof that a caller-selected state base
  is outside a detected installation's version directory; the trusted runtime
  resolver must corroborate placement before enrollment.
- This store saves typed records and revisions; it does NOT atomically drive
  Start/Apply and active-operation selection yet. The state-machine adapter is
  the next persistence slice. Dismissal serialization exists but durable dismissal
  storage is not yet wired into this primitive. No live preferences are written.
- No production migration is implemented; unsupported older/future database
  schemas are refused, not upgraded. Native migration/recovery acceptance stays
  pending. Database transaction success does not prove filesystem activation.
- Root trust, operator credentials, real agent lifecycles, package registration,
  conversation storage and existing installations have not been changed.
