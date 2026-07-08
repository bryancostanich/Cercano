package meridian

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultVersion is the pinned @rynfar/meridian npm version Cercano spawns.
// Bumping this is a deliberate act — verify the upstream changelog and
// re-test the full Claude Max round-trip first.
const DefaultVersion = "1.45.0"

// State is the lifecycle phase of the Meridian subprocess from Cercano's
// point of view. The transitions are: Disabled → (PrereqsMissing | NeedsAuth
// | External | Starting), Starting → (Ready | Failed), Ready → Failed (crash)
// or Disabled (Stop). External means some other Meridian (or OpenCode) is
// already on the port; we reuse it but don't manage its lifecycle. A watcher
// polls the port while External — if the external process dies, the manager
// re-runs Ensure and spawns its own (External → Starting).
type State int

const (
	StateDisabled       State = iota // not requested, or Stop() was called
	StatePrereqsMissing              // Node.js too old / missing
	StateNeedsAuth                   // no Claude OAuth token in keychain
	StateStarting                    // subprocess spawned, probe not yet succeeded
	StateReady                       // probe succeeded; meridian is serving
	StateFailed                      // spawn or probe failed
	StateExternal                    // another process owns the port; we did not spawn
)

func (s State) String() string {
	switch s {
	case StateDisabled:
		return "disabled"
	case StatePrereqsMissing:
		return "prereqs_missing"
	case StateNeedsAuth:
		return "needs_auth"
	case StateStarting:
		return "starting"
	case StateReady:
		return "ready"
	case StateFailed:
		return "failed"
	case StateExternal:
		return "external"
	}
	return "unknown"
}

// Status is a point-in-time snapshot of the manager.
type Status struct {
	State       State
	Message     string   // human-readable detail (error, hint, version, etc.)
	Port        int      // port we manage / are reusing; 0 if Disabled
	MissingDeps []string // populated when State == StatePrereqsMissing
}

// Manager owns the local Meridian subprocess. Construct with New, then call
// Ensure when the active cloud profile needs Meridian and Stop on shutdown.
// All exported methods are safe for concurrent use.
type Manager struct {
	logger  *log.Logger
	logPath string // where the subprocess's stdout/stderr is teed (~/.cercano/meridian.log)
	version string

	// Injection points for tests. In production these are realSpawn / realProbe /
	// DetectPrereqs / HasClaudeAuth / portInUse.
	spawnFn    func(ctx context.Context, port int, version string, logSink io.Writer) (*exec.Cmd, error)
	probeFn    func(ctx context.Context, port int) error
	prereqsFn  func() Prereqs
	authFn     func() bool
	portUsedFn func(port int) bool
	// reapOrphanFn reaps a Meridian we previously spawned that a hard-killed
	// agent left orphaned on the port (its pid is recorded in the pidfile).
	// Returns true when it reaped one — the caller then spawns a fresh process
	// instead of adopting the orphan as external, so spawn-time config applies.
	// Injection point for tests.
	reapOrphanFn func(port int) bool
	// identifyGroupFn reports whether the live process group behind a recorded
	// pid still looks like a Meridian we spawned. Guards the reaper against
	// recycled pids: never kill what we can't identify as ours. Injection
	// point for tests; production is groupLooksLikeMeridian.
	identifyGroupFn func(pid int) bool

	mu                  sync.Mutex
	status              Status
	cancel              context.CancelFunc // cancels the active supervisor; nil when none
	cmd                 *exec.Cmd          // the spawned meridian process, if we own it
	listener            func(Status)
	lastPort            int                // last port requested via Ensure (for poller retries)
	lastCtx             context.Context    // ctx of the last Ensure (for poller retries)
	authPollCancel      context.CancelFunc // cancels the auth poller; nil when none
	externalWatchCancel context.CancelFunc // cancels the external-port watcher; nil when none
	failedRetryCancel   context.CancelFunc // cancels the pending failed-state retry; nil when none
}

// authPollInterval is how often the manager re-checks the keychain while in
// NeedsAuth state. Tight enough that the user runs `claude login` and the
// chip clears within a few seconds; loose enough not to spam /usr/bin/security.
var authPollInterval = 3 * time.Second

// externalPollInterval is how often the manager re-checks the port while in
// External state. The check is a local TCP bind attempt — cheap enough to run
// often, and a dead external Meridian means every cloud call is failing, so
// recovery should be quick.
var externalPollInterval = 5 * time.Second

// failedRetryInterval is how long the manager waits in Failed state before
// re-running Ensure on its own. Long enough not to hammer npx on a persistent
// failure; short enough that a transient one (network blip during the npm
// fetch, port squatter that went away) self-heals without user action.
var failedRetryInterval = 30 * time.Second

// New constructs a Manager. logPath should be the file to tee Meridian's
// stdout/stderr into (typically ~/.cercano/meridian.log). Empty logPath
// silently discards subprocess output.
func New(logger *log.Logger, logPath string) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	m := &Manager{
		logger:     logger,
		logPath:    logPath,
		version:    DefaultVersion,
		spawnFn:    realSpawn,
		probeFn:    realProbe,
		prereqsFn:  DetectPrereqs,
		authFn:     HasClaudeAuth,
		portUsedFn: portInUse,
		status:     Status{State: StateDisabled},
	}
	m.reapOrphanFn = m.realReapOrphan
	m.identifyGroupFn = groupLooksLikeMeridian
	return m
}

// groupLooksLikeMeridian reports whether the process group led by pgid has a
// member whose command line mentions meridian (npx's argv carries
// @rynfar/meridian; the spawned binary's path carries node_modules/.bin/meridian).
func groupLooksLikeMeridian(pgid int) bool {
	return exec.Command("pgrep", "-f", "-g", strconv.Itoa(pgid), "meridian").Run() == nil
}

// killGroupOrProcess kills the process's entire group (realSpawn puts the npx
// → Meridian chain in its own group via Setpgid, keyed by the npx pid), falling
// back to a single-process kill for processes without their own group. Killing
// only the direct child leaves the Meridian grandchild holding the port — that
// is exactly how orphans were being created.
func killGroupOrProcess(proc *os.Process) {
	if proc == nil {
		return
	}
	if err := syscall.Kill(-proc.Pid, syscall.SIGKILL); err != nil {
		_ = proc.Kill()
	}
}

// pidFilePath is where the spawned Meridian's pid is recorded, next to the
// log. Empty when logPath is unset (tests), which disables pidfile tracking.
func (m *Manager) pidFilePath() string {
	if m.logPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.logPath), "meridian.pid")
}

// writePidFile records the spawned Meridian's group pid plus the spawner's
// own pid at path, one per line (no-op on empty path). The spawner pid is what
// lets a concurrent Manager tell an orphan from a sibling: multiple cercano
// processes (the agent singleton, plus one embedded server per --mcp instance)
// share this pidfile and the port, and "the group looks like Meridian" is true
// for a healthy sibling's proxy too. Only a dead spawner makes it an orphan.
// The error matters: a pid we failed to record is an orphan the reaper can
// never find.
func writePidFile(path string, pid, spawnerPid int) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path,
		[]byte(strconv.Itoa(pid)+"\n"+strconv.Itoa(spawnerPid)+"\n"), 0o644)
}

// readPidFile reads the Meridian group pid (line 1) and the spawner pid
// (line 2) from path. Returns ok=false on empty path, missing file, or a
// malformed group pid. spawnerPid is 0 when unknown — a legacy one-line file
// from an older build, or a malformed second line; callers must treat unknown
// as "possibly a live sibling" and never reap on it.
func readPidFile(path string) (pid, spawnerPid int, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	pid, err = strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 {
		return 0, 0, false
	}
	if len(lines) > 1 {
		if sp, err := strconv.Atoi(strings.TrimSpace(lines[1])); err == nil && sp > 0 {
			spawnerPid = sp
		}
	}
	return pid, spawnerPid, true
}

// removePidFile deletes the pidfile (best-effort; no-op on empty path). Only
// for callers that just validated the file's contents themselves (the reaper);
// teardown paths cleaning up their own spawn must use removePidFileIfOwned.
func removePidFile(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// removePidFileIfOwned deletes the pidfile only when it still names pid — the
// process the caller spawned and is now tearing down. Unconditional removal
// here is how siblings corrupted each other: A spawns and records its pid; B
// reaps A's Meridian and records ITS pid; A's supervise wakes from cmd.Wait()
// and, seeing only its own in-memory m.cmd pointer, would delete the file that
// now names B's live Meridian — destroying B's orphan tracking. This is
// best-effort coordination (read-then-remove, no file lock; a true lock is a
// possible follow-up under design discussion): the goal is that concurrent
// Managers never kill a sibling's healthy Meridian and never delete a pidfile
// they don't own.
func removePidFileIfOwned(path string, pid int) {
	if path == "" {
		return
	}
	recorded, _, ok := readPidFile(path)
	if !ok || recorded != pid {
		return
	}
	_ = os.Remove(path)
}

// realReapOrphan is the production reapOrphanFn. The pidfile records the pid
// of the npx process we spawned, which (via Setpgid) is also its process-group
// id — the group covers npx plus the Meridian binary it runs. When that group
// is still alive AND its spawner is dead AND it still identifies as Meridian,
// it's ours, orphaned by a hard-killed agent: kill the whole group so a fresh
// spawn applies current spawn-time config, and report true. Returns false when
// nothing of ours is recorded, the group is gone, the spawner is still alive
// (a healthy sibling's Meridian — several cercano processes share this port
// and pidfile), or the pid was recycled to something we can't identify — in
// those cases the caller adopts whatever is on the port as a genuinely
// external proxy.
func (m *Manager) realReapOrphan(port int) bool {
	path := m.pidFilePath()
	pid, spawner, ok := readPidFile(path)
	if !ok {
		return false
	}
	// Signal 0 to the group: any member still alive?
	if syscall.Kill(-pid, 0) != nil {
		removePidFile(path)
		return false
	}
	// A legacy one-line pidfile (older build) has an unknown spawner. Be
	// conservative: leave the file alone and let the caller adopt the process
	// as External — safety over spawn-time-config freshness. If it dies later,
	// the External watcher notices and we spawn our own.
	if spawner == 0 {
		return false
	}
	// Spawner still alive → this is a live sibling cercano's healthy Meridian,
	// not an orphan. Killing it starts the reap war: the sibling sees "exited
	// unexpectedly" → Failed, and its retry reaps ours right back. Don't touch
	// the pidfile either — the sibling owns it. A pid recycled to something
	// alive lands here too; false-alive is the safe direction.
	if syscall.Kill(spawner, 0) == nil {
		return false
	}
	// Alive — but is it still ours? A stale pidfile (crash before cleanup)
	// plus pid recycling can point at an innocent process. Never kill what we
	// can't identify as Meridian.
	if !m.identifyGroupFn(pid) {
		removePidFile(path)
		m.logger.Printf("meridian: recorded pid %d is alive but not a Meridian (recycled pid?); leaving it alone", pid)
		return false
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	removePidFile(path)
	m.logger.Printf("meridian: reaped orphaned Meridian group %d holding port %d", pid, port)
	return true
}

// SetStatusListener registers a callback fired on every state transition.
// The callback runs synchronously on the goroutine that triggered the change;
// keep it short (push to a channel or broadcast and return).
func (m *Manager) SetStatusListener(fn func(Status)) {
	m.mu.Lock()
	m.listener = fn
	m.mu.Unlock()
}

// Status returns a snapshot of the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Ensure asks the manager to ensure Meridian is up on the given port. It
// checks prereqs and auth first; if either is missing, it sets a terminal
// state and returns without spawning. If something is already listening on
// the port, it adopts External state (someone else owns the proxy). Otherwise
// it kicks off a supervisor goroutine that spawns Meridian and watches it.
//
// Idempotent. Calling Ensure(ctx, sameport) while we're already Ready on
// that port is a no-op. Calling Ensure with a different port stops the
// current supervisor and starts a new one.
func (m *Manager) Ensure(ctx context.Context, port int) {
	m.mu.Lock()
	m.lastPort = port
	m.lastCtx = ctx
	// Already managing this exact port and not in a terminal-fail state — nothing to do.
	if m.status.Port == port &&
		(m.status.State == StateStarting || m.status.State == StateReady) {
		m.mu.Unlock()
		return
	}
	// External adoption only holds while the external process is actually
	// there. If it died, fall through and re-run the gates (which will now
	// find the port free and spawn our own).
	if m.status.Port == port && m.status.State == StateExternal && m.portUsedFn(port) {
		m.mu.Unlock()
		return
	}
	// Tear down any prior supervisor (different port, or recovering from Failed).
	m.stopLocked()

	// Gate 1: prereqs.
	pre := m.prereqsFn()
	if !pre.NodeOK {
		m.setStatusLocked(Status{
			State:       StatePrereqsMissing,
			Message:     "Node.js " + strconv.Itoa(minNodeMajor) + "+ required to run Meridian",
			Port:        port,
			MissingDeps: pre.MissingNotes,
		})
		m.mu.Unlock()
		return
	}
	// Gate 2: claude OAuth token.
	if !m.authFn() {
		m.setStatusLocked(Status{
			State:   StateNeedsAuth,
			Message: "Sign in to Claude to start Meridian",
			Port:    port,
		})
		m.startAuthPollerLocked(ctx)
		m.mu.Unlock()
		return
	}
	// Gate 3: something already on the port?
	if m.portUsedFn(port) {
		// If it's our own Meridian, orphaned by a hard-killed agent that never
		// ran a clean Stop, reap it so a fresh spawn picks up current spawn-time
		// config; then fall through to spawn our own. Otherwise it's genuinely
		// someone else's proxy — adopt and watch it (if it dies, the watcher
		// re-runs Ensure so we spawn our own).
		if !m.reapOrphanFn(port) {
			m.setStatusLocked(Status{
				State:   StateExternal,
				Message: fmt.Sprintf("Meridian already running on port %d (not managed by Cercano)", port),
				Port:    port,
			})
			m.startExternalWatcherLocked(ctx)
			m.mu.Unlock()
			return
		}
		// Reaped our orphan — fall through to spawn our own below. supervise
		// waits for the port to free before binding.
	}
	// Start a supervisor for our own process.
	sctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.setStatusLocked(Status{
		State:   StateStarting,
		Message: "starting Meridian via npx",
		Port:    port,
	})
	m.mu.Unlock()

	go m.supervise(sctx, port)
}

// Stop tears down the supervisor and the subprocess, returning to Disabled.
// Safe to call at any time; cheap when already Disabled.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	m.setStatusLocked(Status{State: StateDisabled})
}

// stopLocked cancels the supervisor and kills the spawned process. Caller
// must hold m.mu. Does NOT change status — caller decides what to set it to.
func (m *Manager) stopLocked() {
	m.stopAuthPollerLocked()
	m.stopExternalWatcherLocked()
	m.stopFailedRetryLocked()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.cmd != nil && m.cmd.Process != nil {
		pid := m.cmd.Process.Pid
		killGroupOrProcess(m.cmd.Process)
		m.cmd = nil
		// Our process is dead by our own hand — drop the pidfile so a later
		// agent never mistakes a recycled pid for our orphan. Only if it still
		// names our pid: a sibling may have reaped us and recorded its own
		// Meridian there, and that record is not ours to destroy.
		removePidFileIfOwned(m.pidFilePath(), pid)
	}
}

// startAuthPollerLocked spawns a background goroutine that re-checks the
// keychain every authPollInterval. When auth appears (the user has run
// `claude login`), it calls Ensure to drive the state machine forward
// without requiring an explicit retry from the client. Caller must hold m.mu.
//
// Idempotent: starting a poller while one is already running cancels the
// previous instance so we never run two at once. Stopped by
// stopAuthPollerLocked — which is in turn called from stopLocked, so every
// path that tears the manager down (Stop, Ensure transitions, etc.) also
// drops the poller.
func (m *Manager) startAuthPollerLocked(parent context.Context) {
	if m.authPollCancel != nil {
		m.authPollCancel()
	}
	pctx, cancel := context.WithCancel(parent)
	m.authPollCancel = cancel
	port := m.lastPort
	interval := authPollInterval
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-t.C:
				if m.authFn() {
					m.Ensure(parent, port)
					return
				}
			}
		}
	}()
}

// stopAuthPollerLocked cancels any running auth poller. Caller must hold m.mu.
func (m *Manager) stopAuthPollerLocked() {
	if m.authPollCancel != nil {
		m.authPollCancel()
		m.authPollCancel = nil
	}
}

// startExternalWatcherLocked spawns a background goroutine that re-checks the
// adopted port every externalPollInterval while we're in External state. If
// the external process goes away, it calls Ensure to drive the state machine
// forward — Ensure will find the port free and spawn our own Meridian. Caller
// must hold m.mu.
//
// Idempotent: starting a watcher while one is already running cancels the
// previous instance. Stopped by stopExternalWatcherLocked — called from
// stopLocked, so Stop and every Ensure transition also drop the watcher.
func (m *Manager) startExternalWatcherLocked(parent context.Context) {
	if m.externalWatchCancel != nil {
		m.externalWatchCancel()
	}
	wctx, cancel := context.WithCancel(parent)
	m.externalWatchCancel = cancel
	port := m.lastPort
	interval := externalPollInterval
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-wctx.Done():
				return
			case <-t.C:
				if !m.portUsedFn(port) {
					m.logger.Printf("meridian: external process on port %d went away; respawning", port)
					m.Ensure(parent, port)
					return
				}
			}
		}
	}()
}

// stopExternalWatcherLocked cancels any running external-port watcher. Caller
// must hold m.mu.
func (m *Manager) stopExternalWatcherLocked() {
	if m.externalWatchCancel != nil {
		m.externalWatchCancel()
		m.externalWatchCancel = nil
	}
}

// failWithRetry marks the manager Failed and schedules a one-shot re-Ensure
// after failedRetryInterval, so Failed is not a dead end (NeedsAuth has the
// auth poller, External has the port watcher — this is the same idea). If the
// retry fails again, its supervise schedules the next one.
//
// No-op when the state machine has already moved on — a Stop set Disabled, or
// a replacing Ensure is managing a different port — so a stale supervisor's
// late failure can't clobber the current status or resurrect Meridian.
func (m *Manager) failWithRetry(s Status) {
	m.mu.Lock()
	if m.status.State == StateDisabled || m.lastPort != s.Port {
		m.mu.Unlock()
		return
	}
	m.setStatusLocked(s)
	m.startFailedRetryLocked()
	m.mu.Unlock()
}

// startFailedRetryLocked schedules a single re-Ensure. Derives from the last
// Ensure's context — NOT the supervisor's, which the retry's own Ensure
// cancels via stopLocked. Caller must hold m.mu.
func (m *Manager) startFailedRetryLocked() {
	if m.failedRetryCancel != nil {
		m.failedRetryCancel()
	}
	if m.lastCtx == nil {
		return
	}
	rctx, cancel := context.WithCancel(m.lastCtx)
	m.failedRetryCancel = cancel
	parent, port, interval := m.lastCtx, m.lastPort, failedRetryInterval
	go func() {
		t := time.NewTimer(interval)
		defer t.Stop()
		select {
		case <-rctx.Done():
			return
		case <-t.C:
			if m.Status().State != StateFailed {
				return
			}
			m.logger.Printf("meridian: retrying after failure (port %d)", port)
			m.Ensure(parent, port)
		}
	}()
}

// stopFailedRetryLocked cancels any pending failed-state retry. Caller must
// hold m.mu.
func (m *Manager) stopFailedRetryLocked() {
	if m.failedRetryCancel != nil {
		m.failedRetryCancel()
		m.failedRetryCancel = nil
	}
}

// supervise runs in its own goroutine for one Ensure() invocation. It
// spawns Meridian, polls until the port responds, marks Ready, then waits
// for the process to exit. On unexpected exit it marks Failed and stops
// (no auto-restart in v1; the user re-triggers via /config or restart).
func (m *Manager) supervise(ctx context.Context, port int) {
	logSink := openLogSink(m.logPath, m.logger)
	defer func() {
		if c, ok := logSink.(io.Closer); ok {
			_ = c.Close()
		}
	}()

	// After reaping an orphan the OS may not have released the listening socket
	// yet; wait briefly for the port to free before binding. No-op on the
	// normal path, where Gate 3 already saw the port free.
	for i := 0; i < 40 && m.portUsedFn(port); i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}

	cmd, err := m.spawnFn(ctx, port, m.version, logSink)
	if err != nil {
		m.failWithRetry(Status{
			State:   StateFailed,
			Message: "failed to spawn Meridian: " + err.Error(),
			Port:    port,
		})
		return
	}
	// Record the pid (also the process-group id, via Setpgid) plus our own pid
	// as spawner, so a later agent can recognize and reap an orphan — but only
	// once we (the spawner) are dead; while we live it's our healthy Meridian,
	// not an orphan. Written in the same critical section that publishes m.cmd,
	// so a concurrent Stop can't remove the pidfile and then have us re-create
	// it naming a dead pid.
	m.mu.Lock()
	m.cmd = cmd
	if cmd.Process != nil {
		if path := m.pidFilePath(); path == "" {
			m.logger.Printf("meridian: pidfile tracking disabled (no log path) — an orphaned proxy won't be auto-reaped")
		} else if err := writePidFile(path, cmd.Process.Pid, os.Getpid()); err != nil {
			m.logger.Printf("meridian: cannot write pidfile %s: %v — an orphaned proxy won't be auto-reaped", path, err)
		}
	}
	m.mu.Unlock()

	// Probe for readiness. 60s budget covers cold-cache npx fetch + Node boot.
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := waitForReady(probeCtx, port, m.probeFn); err != nil {
		// Probe failed (timeout or ctx cancellation). Kill OUR spawn's group so
		// we don't leak it — never m.cmd, which a replacing Ensure may already
		// have repointed at a fresh process. Drop the pidfile only if it is
		// still ours: it names a pid we just killed, and a stale pidfile is
		// what makes recycled-pid reaping possible.
		killGroupOrProcess(cmd.Process)
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
			removePidFileIfOwned(m.pidFilePath(), cmd.Process.Pid)
		}
		m.mu.Unlock()
		m.failWithRetry(Status{
			State:   StateFailed,
			Message: "Meridian failed to become ready: " + err.Error(),
			Port:    port,
		})
		return
	}
	m.transition(Status{
		State:   StateReady,
		Message: "Meridian v" + m.version + " ready on port " + strconv.Itoa(port),
		Port:    port,
	})

	// Wait for the process. On exit, if ctx was cancelled (Stop), we're
	// going to Disabled — the Stop call sets that. Otherwise it's an
	// unexpected exit; drop the now-dead pid's file and mark Failed. Ownership
	// check on the removal: "unexpected exit" includes a sibling cercano
	// having reaped us and recorded its own Meridian in the pidfile — the
	// in-memory m.cmd == cmd guard can't see that, only the file can.
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return // Stop() handles status
	}
	m.mu.Lock()
	if m.cmd == cmd {
		m.cmd = nil
		removePidFileIfOwned(m.pidFilePath(), cmd.Process.Pid)
	}
	m.mu.Unlock()
	msg := "Meridian exited unexpectedly"
	if waitErr != nil {
		msg += ": " + waitErr.Error()
	}
	m.failWithRetry(Status{State: StateFailed, Message: msg, Port: port})
}

// transition atomically updates status and fires the listener.
func (m *Manager) transition(s Status) {
	m.mu.Lock()
	m.setStatusLocked(s)
	m.mu.Unlock()
}

// setStatusLocked writes status and fires the listener. Caller must hold m.mu.
// The listener is invoked while m.mu is held — keep listener short.
func (m *Manager) setStatusLocked(s Status) {
	m.status = s
	if m.listener != nil {
		m.listener(s)
	}
}

// realSpawn launches `npx -y @rynfar/meridian@<version>` with $CLAUDE_PROXY_PORT
// set, piping stdout+stderr into logSink.
func realSpawn(ctx context.Context, port int, version string, logSink io.Writer) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "npx", "-y", "@rynfar/meridian@"+version)
	// Own process group, keyed by the npx pid. npx runs the Meridian binary as
	// a child — killing npx alone leaves Meridian holding the port, which is
	// how orphans were being created. Group membership lets Stop and the
	// reaper take down the whole chain (same pattern as llamaserver).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(),
		"CLAUDE_PROXY_PORT="+strconv.Itoa(port),
		"CLAUDE_PROXY_HOST=127.0.0.1",
		// Disable Meridian's auto-defer tool loading. Cercano presents 34 tools,
		// which trips Meridian's default defer threshold (15) and pushes 28 of
		// them behind the SDK's ToolSearch progressive-discovery path. That churn
		// — per-turn `discovered=` cycling, re-discovery on every stale-session
		// eviction — desynced turn correlation and forced the extra (4th) SDK
		// turn. Loading all tools up front removes the churn; 34 small tools in
		// the (prompt-cached) system prompt is a modest cost. Appended last so it
		// wins over any inherited value. See docs/agent/meridian-opencode-route.md.
		"MERIDIAN_DEFER_TOOL_THRESHOLD=0",
	)
	cmd.Stdout = logSink
	cmd.Stderr = logSink
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// realProbe sends a quick HTTP request to the proxy. Any HTTP response (even
// a 404) proves the process is serving; only network-level failures count
// as "not ready".
func realProbe(ctx context.Context, port int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://127.0.0.1:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// waitForReady polls probeFn until it returns nil or ctx expires. 200ms cadence
// keeps the startup probe cheap; the ctx supplies the overall budget.
func waitForReady(ctx context.Context, port int, probe func(context.Context, int) error) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	// First probe immediately — fast meridian builds can be ready in <100ms.
	if err := probe(ctx, port); err == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if err := probe(ctx, port); err == nil {
				return nil
			}
		}
	}
}

// portInUse returns true if anything is currently listening on 127.0.0.1:port.
// We try to bind; if bind fails with EADDRINUSE (any error here, really),
// something owns the port.
func portInUse(port int) bool {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}

// openLogSink opens (or creates+truncates) the log file Meridian's stdout/stderr
// is teed to. If logPath is empty or the open fails, returns io.Discard so the
// supervisor still runs.
func openLogSink(logPath string, logger *log.Logger) io.Writer {
	if logPath == "" {
		return io.Discard
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		logger.Printf("meridian: log dir %s: %v (discarding subprocess output)", filepath.Dir(logPath), err)
		return io.Discard
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		logger.Printf("meridian: log file %s: %v (discarding subprocess output)", logPath, err)
		return io.Discard
	}
	return f
}
