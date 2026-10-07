# Independent one-shot utility launch — native verification

Slice scope: the narrow process-lifetime primitive that starts the approved
one-shot update utility as an independent process. No service, listener,
control credential, updater daemon or new binary entrypoint was added.
Normal client/agent connections and the existing procx/agentclient launch paths
are unchanged.

What exists now: `source/server/internal/updatecoord/launch`. `Launch(Options)`
takes an EXPLICIT trusted absolute executable path and a FIXED caller-built
argv, and nothing else resolvable. No PATH, no user-home or default-binary
resolution, no shell, no command or argument ever taken from update/release
metadata. Relative, missing and directory executables fail closed
(`ErrInvalidExecutable`), unsupported platforms fail closed
(`ErrUnsupportedPlatform`), and unopenable output logs fail closed before any
child starts (`ErrOutputSetup`).

Independence and output are deliberate:

- Unix: the child starts in its own session (setsid, the existing agentclient
  detach pattern), so the initiating parent's exit, terminal and process-group
  signals cannot reach it. Windows: the child starts with constant flags
  CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS, so console control events and
  console close are not inherited. CREATE_BREAKAWAY_FROM_JOB is deliberately
  not set: escape from a restrictive job object is neither claimed nor
  attempted.
- stdin is always the platform null device. stdout/stderr go to caller-owned
  regular log files (create/append) or to the null device. Never a
  parent-owned pipe: the child's writes can never block on the parent and
  parent stdio closure can neither block nor terminate the child, so output
  failure cannot silently terminate an update.
- The launcher never waits for, cancels, signals or supervises the child, and
  acquires no lock: a parent never holds an update lease its child needs. The
  utility process must acquire its own updatecoord/exclusion lease when that
  composition is built.

Local native verification (macOS, darwin/arm64, go1.26.3): `go vet`, `gofmt`,
`go test -race -count=3`, `go build ./...` and the full `./internal/updatecoord/...`
suite pass; `GOOS=windows` and `GOOS=linux` vet and build pass. The owned Go
testprocess fixtures (all `TestOneShotLaunch_...` prefixed) cover: relative /
missing / directory executables rejected without PATH fallback; output-log
setup failure fails closed; log files receive child stdout and stderr
(reaped direct child); 4 MiB of discarded output never blocks; and the chain
test — intermediate parent launches the child through the primitive, cancels
its own context, closes its own stdio and exits (or is hard-killed) — after
which the child writes its log line and completion marker. On Unix the child
also proves it holds its own session and process group. Every helper the
fixtures start is reaped or terminated on every path; success paths never
signal an exited pid again.

The existing updatecoord-tests workflow (native Windows/Linux/macOS matrix,
race step, vet, compile-only sweep) already covers the new package via its
`source/server/internal/updatecoord/**` path filter and
`./internal/updatecoord/...` package selectors. No workflow, routing or budget
configuration was changed. These results are native-local; the branch's usual
native CI run remains the recorded matrix for this slice.

## Exact remaining bootstrap/privilege constraints

- The primitive verifies only path usability. Production callers MUST resolve
  and verify the executable and its installation beforehand; nothing here
  proves file authenticity, signing, package-manager ownership or defense
  against a hostile same-user owner replacing directories. The utility binary
  itself, its entrypoint and any process-copy/bootstrap of the update onto the
  installed path are still unimplemented and unscheduled.
- Same-user execution is assumed. No privilege separation exists: the child
  runs with the initiating app's credentials. No admin-token management has
  been implemented or claimed.
- Windows: a parent inside a restrictive job object that kills children on job
  close still owns the child; job escape is NOT provided. Detachment flags are
  exercised by tests, but native Windows runs come only from the CI matrix,
  not from local verification. Unix: setsid detaches the session, but
  launchd/systemd session teardown or container init behavior in a real
  desktop session remains a bootstrap-review item, not a test-verified claim.
- Lease handoff is still compositional only: the child acquiring its own
  exclusion lease is expected by contract; wiring, admission barriers, stale
  PID validation and failure cleanup when the utility dies before or during
  lease acquisition remain unimplemented.
