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
metadata. Relative, missing and NON-REGULAR executables (directory, FIFO,
device) fail closed (`ErrInvalidExecutable`), unsupported platforms fail
closed (`ErrUnsupportedPlatform`), and output logs fail closed before any
child starts: a relative, symlinked, FIFO, directory or otherwise non-regular
log path is refused (`ErrInvalidOutputPath`), an unopenable one fails
(`ErrOutputSetup`), and newly created logs are owner-only 0600. The log-path
parent directory is assumed trusted and caller-owned: this defends against
misconfiguration and accidents, not against a hostile same user (no stronger
same-user TOCTOU guarantee is claimed).

Independence and output are deliberate:

- Unix: the child starts in its own session (setsid, the existing agentclient
  detach pattern), so the initiating parent's exit, terminal and process-group
  signals cannot reach it. Windows: the child starts with constant flags
  CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS | CREATE_BREAKAWAY_FROM_JOB.
  Detachment alone does not make the child outlive a parent job that kills on
  close, so the breakaway is attempted explicitly and FAILS CLOSED: when the OS
  forbids it (a restrictive job in the chain without
  JOB_OBJECT_LIMIT_BREAKAWAY_OK), CreateProcess refuses and Launch surfaces
  that error — no silent fallback to a job-sharing child and no attempt to
  change any CI or global job policy.
- stdin is always the platform null device. stdout/stderr go to caller-owned
  ABSOLUTE regular log files (create/append 0600, checked regular before and
  after open, opened without following final-component symlinks and without
  blocking on FIFOs where the platform provides the flags) or to the null
  device. Never a parent-owned pipe: the child's writes can never block on the
  parent and parent stdio closure can neither block nor terminate the child,
  so output failure cannot silently terminate an update.
- The launcher never cancels, signals or supervises the child, and acquires
  no lock: a parent never holds an update lease its child needs. Its ONLY
  wait is a fire-and-forget reaper goroutine that reaps an exited child while
  the initiating parent is alive (an unreaped zombie must not linger on
  native Unix); if the parent exits first, the goroutine dies with it and the
  kernel reparents the child, which platform init reaps. The utility process
  must acquire its own updatecoord/exclusion lease when that composition is
  built.

Local native verification (macOS, darwin/arm64, go1.26.3): `go vet`, `gofmt`,
`go test -race -count=3`, `go build ./...` and the full `./internal/updatecoord/...`
suite pass; `GOOS=windows` (including test-binary compile), `GOOS=linux` and
`GOOS=plan9`/`GOOS=js` vet and build pass. The owned Go
testprocess fixtures (all `TestOneShotLaunch_...` prefixed) cover: relative /
missing / non-regular executables rejected without PATH fallback; relative,
symlink, FIFO and directory log paths rejected (FIFO fixtures keep an owned
RDWR keeper so no pre-fix hang can wedge a test); output-log setup failure
fails closed with no child started; log files receive child stdout and stderr
(reaped direct child), newly created logs are 0600; 4 MiB of discarded output
never blocks; the reaping contract — a short child is reaped while the
initiating parent is still alive, evidenced natively (kill(0) transition to
ESRCH plus wait4(WNOHANG) ECHILD agreement, no test-side wait and no
post-reap signaling of a possibly recycled pid) — and the chain
test — intermediate parent launches the child through the primitive, cancels
its own context, closes its own stdio and exits (or is hard-killed) — after
which the child writes its log line and completion marker. On Unix the child
also proves it holds its own session and process group. Every helper the
fixtures start is reaped or terminated on every path; success paths never
signal an exited pid again.

Windows job-object fixtures (`launch_job_windows_test.go`, each job owned by
its fixture process, no CI/global job created or modified) encode the
breakaway contract: a restrictive owned job (kill-on-close, no
BREAKAWAY_OK) must make Launch fail closed with the OS error surfaced and no
child started (refusals are classified; only an access-denied breakaway
denial counts as the intended outcome), and a breakaway-permitting owned job
must produce a child that completes after its own parent's job handle closes
and the job terminates. If the host environment itself forbids the breakaway
(outer job chain), the allow test classifies the refusal and SKIPS: no escape
is claimed where none was proven. These fixtures COMPILE on Windows
(`GOOS=windows go test -c`) but their RUN evidence comes only from the
native Windows CI matrix — no local native Windows execution was performed.

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
- Windows: fail-closed job independence is now implemented (breakaway flag
  with surfaced refusal, no silent fallback, no policy bypass), but it is
  verified only by compile plus the fixtures above; native RUN evidence comes
  from the CI matrix, not local verification, so outliving a kill-on-close
  parent job is not yet a native-proven claim. Note: the OS may refuse the
  DETACHED_PROCESS + CREATE_NEW_PROCESS_GROUP + CREATE_BREAKAWAY_FROM_JOB
  flag combination (CREATE_SUSPENDED is documented as invalid there, and the
  detached/group combination is disputed by some references); the fixtures
  classify any such refusal explicitly instead of counting it as success.
- Unix: setsid detaches the session, but launchd/systemd session teardown or
  container init behavior in a real desktop session remains a
  bootstrap-review item, not a test-verified claim.
- Lease handoff is still compositional only: the child acquiring its own
  exclusion lease is expected by contract; wiring, admission barriers, stale
  PID validation and failure cleanup when the utility dies before or during
  lease acquisition remain unimplemented.
