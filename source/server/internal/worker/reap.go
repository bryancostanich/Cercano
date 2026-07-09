package worker

// reap.go — startup orphan-sweep for worker processes.
//
// A cleanly-shutdown host kills its pooled workers (Task B5). But a HARD-killed
// host (SIGKILL / crash) leaves its workers as orphans: their process groups
// (Setpgid, keyed by the worker pid) survive, and their pidfiles + unix sockets
// linger in $TMPDIR/cercano-workers. ReapOrphanWorkers runs at host startup,
// before any worker is spawned by THIS host, so every pidfile it finds is from a
// PREVIOUS host instance. It kills the ones that are still alive AND positively
// identify as a cercano worker, and cleans up the stale files of the rest.
//
// The identity guard is the safety-critical part, mirroring Meridian's
// groupLooksLikeMeridian: a pidfile is just a number on disk, and the OS recycles
// pids. A stale pidfile whose pid the kernel handed to an innocent process (the
// user's editor, a shell) must NEVER be killed. So a group is reaped only when
// `pgrep -f -g <pgid> "cercano worker"` matches — the worker's own command line.
// A pid we can't identify as a cercano worker is left alone; only its leftover
// files are swept.

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// reapSeams holds the injectable checks the reaper makes about a process group.
// Production wires the real syscall/pgrep implementations; tests substitute fakes
// so the kill DECISION can be exercised without spawning real processes.
type reapSeams struct {
	// groupAlive reports whether any member of the process group led by pgid is
	// still running (production: syscall.Kill(-pgid, 0) == nil).
	groupAlive func(pgid int) bool
	// identifyGroup reports whether the group led by pgid looks like a cercano
	// worker (production: groupLooksLikeWorker via pgrep). The safety guard:
	// false means "do not kill".
	identifyGroup func(pgid int) bool
	// pidAlive reports whether a single pid (the spawner) is still running
	// (production: syscall.Kill(pid, 0) == nil).
	pidAlive func(pid int) bool
	// killGroup kills the whole process group led by pgid (production:
	// killGroupByPgid). Recorded so tests can observe which pgids were reaped.
	killGroup func(pgid int)
}

// realReapSeams is the production wiring.
func realReapSeams() reapSeams {
	return reapSeams{
		groupAlive:    func(pgid int) bool { return syscall.Kill(-pgid, 0) == nil },
		identifyGroup: groupLooksLikeWorker,
		pidAlive:      func(pid int) bool { return syscall.Kill(pid, 0) == nil },
		killGroup:     killGroupByPgid,
	}
}

// ReapOrphanWorkers sweeps the worker pidfile directory and reaps orphaned
// worker process groups left behind by a hard-killed previous host. It is
// best-effort: any error is logged, never returned, so a sweep failure can never
// block host startup. Returns the number of live worker groups it killed.
//
// Call it once at startup, BEFORE spawning any worker of this host.
func ReapOrphanWorkers() int {
	return reapOrphanWorkers(workerDir(), realReapSeams())
}

// workerDir is the pidfile/socket directory ($TMPDIR/cercano-workers).
func workerDir() string {
	return filepath.Join(os.TempDir(), workerSocketDir)
}

// reapOrphanWorkers is the testable core: it scans dir for *.pid files and,
// per file, decides reap / clean-up-only via the injected seams.
func reapOrphanWorkers(dir string, s reapSeams) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Missing dir (never spawned a worker) is the common, healthy case: no-op.
		if !os.IsNotExist(err) {
			log.Printf("[worker] orphan-sweep: cannot read %s: %v", dir, err)
		}
		return 0
	}

	reaped := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		pidPath := filepath.Join(dir, e.Name())
		if reapOnePidfile(pidPath, s) {
			reaped++
		}
	}
	if reaped > 0 {
		log.Printf("[worker] orphan-sweep: reaped %d orphaned worker(s)", reaped)
	}
	return reaped
}

// reapOnePidfile handles a single pidfile. Returns true iff it KILLED a live,
// identity-matched worker group.
//
// Decision table:
//   - unreadable / malformed pidfile     -> leave the file (we can't own it), false
//   - group dead                         -> clean up stale files, false
//   - spawner recorded and still alive   -> live sibling (not an orphan), leave it, false
//   - group alive but NOT identity-match -> recycled/foreign pid: NEVER kill; clean files, false
//   - group alive AND identity-match     -> reap group + remove pidfile + socket, true
func reapOnePidfile(pidPath string, s reapSeams) bool {
	pid, spawner, ok := readWorkerPidFile(pidPath)
	if !ok {
		// Malformed / unparseable: we never validated its contents, so leave it.
		return false
	}

	// Is the process group still alive at all?
	if !s.groupAlive(pid) {
		// Dead worker's leftover files — a hard-killed host that already lost its
		// workers. Sweep the stale pidfile + socket. Do NOT kill (nothing to kill).
		cleanupStaleFiles(pidPath, pid)
		return false
	}

	// A live spawner means the recording host is still running: this is a live
	// sibling's worker, not an orphan. Leave it and its pidfile completely alone.
	// (spawner == 0 is a legacy/unknown-spawner file; fall through to the identity
	// guard, which still protects us from killing a non-worker.)
	if spawner > 0 && s.pidAlive(spawner) {
		return false
	}

	// Alive — but is it actually one of ours? The kernel recycles pids; a stale
	// pidfile can point at an innocent process. Never kill what we can't identify
	// as a cercano worker.
	if !s.identifyGroup(pid) {
		cleanupStaleFiles(pidPath, pid)
		log.Printf("[worker] orphan-sweep: pid %d is alive but not a cercano worker (recycled pid?); leaving the process alone", pid)
		return false
	}

	// Alive AND ours AND no live spawner → a genuine orphan. Reap the whole group.
	s.killGroup(pid)
	cleanupStaleFiles(pidPath, pid)
	log.Printf("[worker] orphan-sweep: reaped orphaned worker group %d", pid)
	return true
}

// cleanupStaleFiles removes a pidfile and its sibling socket. The socket shares
// the pidfile's basename with a ".sock" extension (see workerPaths).
func cleanupStaleFiles(pidPath string, pid int) {
	removePidFile(pidPath)
	sockPath := strings.TrimSuffix(pidPath, ".pid") + ".sock"
	_ = os.Remove(sockPath)
}

// groupLooksLikeWorker reports whether the process group led by pgid has a member
// whose command line is a cercano worker. The worker is spawned as
// `cercano worker --socket <path>` (see spawnWorker), so "cercano worker" is a
// stable substring that identifies it and CANNOT match the host itself
// (`cercano agent` / bare `cercano`) or an unrelated process. This is the
// identity guard: a recycled pid that is not running a cercano worker fails here
// and is never killed.
func groupLooksLikeWorker(pgid int) bool {
	return exec.Command("pgrep", "-f", "-g", strconv.Itoa(pgid), "cercano worker").Run() == nil
}

// removePidFile deletes the pidfile unconditionally (best-effort). Used by the
// reaper, which has already validated the file's contents itself; teardown paths
// cleaning up their own spawn use removePidFileIfOwned instead.
func removePidFile(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// readWorkerPidFile reads the worker group pid (line 1) and spawner pid (line 2)
// from path. Returns ok=false on empty path, missing file, or a malformed group
// pid. spawnerPid is 0 when unknown (a one-line/malformed second line); callers
// must treat unknown as "possibly a live sibling" and rely on the identity guard.
// Mirrors meridian.readPidFile.
func readWorkerPidFile(path string) (pid, spawnerPid int, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 0 {
		return 0, 0, false
	}
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

// killGroupByPgid SIGKILLs the whole process group led by pgid.
func killGroupByPgid(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
