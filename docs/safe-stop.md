# Safe stop (ShutdownAgentWhenIdle)

One-shot, additive stop API on the agent's gRPC service. It waits for observed
idle work, then seals update admission, commits once, and asks the agent's own
process to drain and exit. Old agents (and binaries that predate the method)
answer `ShutdownAgentWhenIdle` with `Unimplemented` — nothing else changes for
them, and the legacy `ShutdownAgent` RPC keeps its existing behavior.

## Identity guard, not authentication

The request carries `expected_pid`, which must be positive. The handler
compares it against the live server process's PID and rejects mismatches
before any effect. **This is an identity guard against stopping the wrong
agent process, not an authentication mechanism.** The PID check proves only
that the request names the process it reaches; it does not authenticate the
caller. Clients must verify actual process identity and installation ownership
out-of-band (the existing client-side helper is the right place to compare
recorded install paths, executable identity, and peer identity before issuing
the request).

## Behavior

- **Busy work is waited on, never cancelled.** An active turn, runtime work,
  credential or auth work, compaction, or hydration keeps the request waiting.
  `waitForUpdateIdle` checks the tracking hooks and waits on retained activity;
  it does not execute compaction or hydration itself. Existing RPC lifetimes and
  the explicitly instrumented asynchronous jobs contribute to that activity.
- **Refusal over guessing.** If any tracking layer cannot prove coverage, or
  the server has no configured stop path, the request is refused
  (`FailedPrecondition`) before any effect. The new method never falls back to
  signaling its own process.
- **Cancel before commit is a full release.** A canceled wait releases the
  pause and any acquired stop slots with no side effects.
- **Commit is atomic and idempotent.** At the moment all work is observed
  idle, the handler seals admission and commits once. Repeat requests while
  committed return `accepted: true` with no duplicate commit and no second stop.
- **Admission stays closed through the actual stop.** The commit boundary is
  the observed-idle seal; it does not release when the RPC returns. Only
  read-only observer streams (`SubscribeEvents`, `AttachConversation`,
  `StreamRuntimeLogs`) keep serving during the drain window. New work fails
  with `Unavailable` until the process actually exits. A context canceled
  after the seal cannot un-commit or unseal; a repeated request simply
  observes the committed state. The existing administrative stop and the new
  idempotent safe-stop method are exempt from ordinary work admission too.
- **Embedded hosts refuse.** An embedded MCP host does not own the standalone
  stop loop. It does not configure this capability and returns FailedPrecondition
  rather than acknowledging a channel request nobody consumes.
- **Process stop path.** The RPC does not signal the process. The agent
  process installs exactly one stop path at startup
  (`SetProcessStopRequester`); on Windows this is the in-process
  `processStopRequest` channel (self-`os.Process.Signal` is unsupported
  there), and OS signals feed the same drain-and-cleanup path as API stops —
  no network listener, no self-kill, and no change to the worker process
  signal protocol.

## Client-side outcomes

The client can never prove "busy" or "left running" from its own deadline:
the server may commit the stop just before the deadline with the
confirmation lost in transit. The client-side helpers therefore type the
endings instead of guessing:

- **Definitive refusal / acceptance.** An accepted response (or an explicit
  refusal such as the PID mismatch or a coverage refusal) is definitive; the
  legacy `ShutdownAgent` bounce is never used as a fallback.
- **`ErrSafeStopUnsupported`** — the agent predates the method and answered
  `Unimplemented`. Definitive skip: the agent was left running, nothing was
  stopped and nothing was started.
- **`ErrSafeStopUncertain`** — the RPC ended in a deadline, cancellation or
  transport (availability) ambiguity. The agent may already have stopped or
  may still be running; this outcome never claims either way on its own.
- **Everything else** propagates as a plain failure; callers must not
  classify arbitrary errors as skip diagnostics.

The brewrestart coordinator resolves `ErrSafeStopUncertain` with a fresh
bounded **local** inspection of the kernel-verified identity (never another
RPC to the possibly-dying agent, never a forced stop): a positively gone PID
continues the existing restart under the held launch lock; the exact same
process still alive is reported as unconfirmed with clear guidance and no
force; an unknown, reused or foreign identity refuses the restart rather
than inventing a PID-reuse proof. If the post-install deadline already
expired, the state is reported as unconfirmed — never "left running" — and
the package update itself is not treated as failed.

