package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// writeWorkerPid writes a two-line worker pidfile (group pid, spawner pid).
func writeWorkerPid(t *testing.T, path string, pid, spawner int) {
	t.Helper()
	if err := writePidFile(path, pid, spawner); err != nil {
		t.Fatalf("writePidFile: %v", err)
	}
}

// touch creates an empty file (a fake socket).
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// captureSeams records every killGroup pgid, with configurable alive/identity.
type captureSeams struct {
	killed     []int
	alivePgids map[int]bool
	ours       map[int]bool
	aliveSpawn map[int]bool
}

func (c *captureSeams) seams() reapSeams {
	return reapSeams{
		groupAlive:    func(pgid int) bool { return c.alivePgids[pgid] },
		identifyGroup: func(pgid int) bool { return c.ours[pgid] },
		pidAlive:      func(pid int) bool { return c.aliveSpawn[pid] },
		killGroup:     func(pgid int) { c.killed = append(c.killed, pgid) },
	}
}

// (a) alive AND identity-matched -> reaped: kill-fn called with the pgid,
// pidfile + socket removed.
func TestReap_AliveAndOurs_Reaped(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "conv-0-aaaa.pid")
	sockPath := filepath.Join(dir, "conv-0-aaaa.sock")
	writeWorkerPid(t, pidPath, 4242, 999) // spawner 999 (dead — see aliveSpawn empty)
	touch(t, sockPath)

	c := &captureSeams{
		alivePgids: map[int]bool{4242: true},
		ours:       map[int]bool{4242: true},
		aliveSpawn: map[int]bool{}, // spawner 999 is dead → genuine orphan
	}
	n := reapOrphanWorkers(dir, c.seams())

	if n != 1 {
		t.Fatalf("reaped count = %d, want 1", n)
	}
	if len(c.killed) != 1 || c.killed[0] != 4242 {
		t.Fatalf("killGroup calls = %v, want [4242]", c.killed)
	}
	if exists(pidPath) {
		t.Error("pidfile should be removed after reap")
	}
	if exists(sockPath) {
		t.Error("socket should be removed after reap")
	}
}

// (b) alive but NOT identity-matched (foreign/recycled pid) -> NOT killed,
// stale files cleaned. THE SAFETY GUARD.
func TestReap_AliveButForeign_NotKilled(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "conv-1-bbbb.pid")
	sockPath := filepath.Join(dir, "conv-1-bbbb.sock")
	writeWorkerPid(t, pidPath, 5555, 0) // unknown spawner → identity guard decides
	touch(t, sockPath)

	c := &captureSeams{
		alivePgids: map[int]bool{5555: true},
		ours:       map[int]bool{}, // alive, but NOT a cercano worker
		aliveSpawn: map[int]bool{},
	}
	n := reapOrphanWorkers(dir, c.seams())

	if n != 0 {
		t.Fatalf("reaped count = %d, want 0", n)
	}
	if len(c.killed) != 0 {
		t.Fatalf("killGroup must NOT be called for a foreign pid, got %v", c.killed)
	}
	if exists(pidPath) || exists(sockPath) {
		t.Error("stale files of an unidentifiable pid should still be cleaned")
	}
}

// (c) group dead -> NOT killed, files cleaned.
func TestReap_DeadGroup_FilesCleaned(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "conv-2-cccc.pid")
	sockPath := filepath.Join(dir, "conv-2-cccc.sock")
	writeWorkerPid(t, pidPath, 6666, 111)
	touch(t, sockPath)

	c := &captureSeams{
		alivePgids: map[int]bool{}, // dead
		ours:       map[int]bool{6666: true},
		aliveSpawn: map[int]bool{},
	}
	n := reapOrphanWorkers(dir, c.seams())

	if n != 0 {
		t.Fatalf("reaped count = %d, want 0", n)
	}
	if len(c.killed) != 0 {
		t.Fatalf("killGroup must NOT be called for a dead group, got %v", c.killed)
	}
	if exists(pidPath) || exists(sockPath) {
		t.Error("a dead worker's leftover files should be cleaned")
	}
}

// A live spawner means a live sibling host owns this worker — leave it alone,
// including its pidfile, even though it would pass the identity check.
func TestReap_LiveSpawner_Spared(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "conv-3-dddd.pid")
	sockPath := filepath.Join(dir, "conv-3-dddd.sock")
	writeWorkerPid(t, pidPath, 7777, 222)
	touch(t, sockPath)

	c := &captureSeams{
		alivePgids: map[int]bool{7777: true},
		ours:       map[int]bool{7777: true},
		aliveSpawn: map[int]bool{222: true}, // spawner alive → live sibling
	}
	n := reapOrphanWorkers(dir, c.seams())

	if n != 0 || len(c.killed) != 0 {
		t.Fatalf("a worker with a live spawner must not be reaped (n=%d killed=%v)", n, c.killed)
	}
	if !exists(pidPath) {
		t.Error("a live sibling's pidfile must be left intact")
	}
}

// (d) empty / missing dir -> no-op, no error, no kills.
func TestReap_MissingDir_NoOp(t *testing.T) {
	c := &captureSeams{}
	// Missing directory.
	if n := reapOrphanWorkers(filepath.Join(t.TempDir(), "does-not-exist"), c.seams()); n != 0 {
		t.Fatalf("missing dir reaped %d, want 0", n)
	}
	// Empty directory.
	if n := reapOrphanWorkers(t.TempDir(), c.seams()); n != 0 {
		t.Fatalf("empty dir reaped %d, want 0", n)
	}
	if len(c.killed) != 0 {
		t.Fatalf("no kills expected, got %v", c.killed)
	}
}

// A malformed pidfile is left in place and never reaped.
func TestReap_MalformedPidfile_LeftAlone(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "conv-4-eeee.pid")
	if err := os.WriteFile(pidPath, []byte("not-a-pid\n123"), 0o644); err != nil {
		t.Fatalf("write malformed pidfile: %v", err)
	}
	c := &captureSeams{}
	n := reapOrphanWorkers(dir, c.seams())
	if n != 0 || len(c.killed) != 0 {
		t.Fatalf("malformed pidfile must not reap (n=%d killed=%v)", n, c.killed)
	}
	if !exists(pidPath) {
		t.Error("a malformed pidfile we never validated should be left in place")
	}
}

// The production identity guard: a group whose command line is a cercano
// worker matches; a plain process does not. Uses real short-lived processes in
// their own groups (no injected seam) to prove the pgrep pattern is precise
// enough not to kill non-workers.
func TestGroupLooksLikeWorker(t *testing.T) {
	startGroup := func(argv ...string) *exec.Cmd {
		t.Helper()
		c := exec.Command(argv[0], argv[1:]...)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			t.Fatalf("start %v: %v", argv, err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL) })
		return c
	}

	// A fake binary named "cercano" that ignores its args and just sleeps, so a
	// real process carries the command line "…/cercano worker --socket …".
	dir := t.TempDir()
	bin := filepath.Join(dir, "cercano")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake cercano: %v", err)
	}

	fake := startGroup(bin, "worker", "--socket", "/tmp/x.sock")
	if !groupLooksLikeWorker(fake.Process.Pid) {
		t.Error("a group running `cercano worker …` should be identified as ours")
	}

	// A plain sleep must NOT match — the guard can't kill an unrelated process.
	plain := startGroup("sleep", "30")
	if groupLooksLikeWorker(plain.Process.Pid) {
		t.Error("a plain sleep group must not be identified as a cercano worker")
	}

	// `cercano agent` (the host itself) must NOT match — precise on "worker".
	host := startGroup(bin, "agent")
	if groupLooksLikeWorker(host.Process.Pid) {
		t.Error("a `cercano agent` host must not be identified as a worker")
	}
}

// readWorkerPidFile round-trips both pids; unknown spawner parses as 0.
func TestReadWorkerPidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.pid")
	if _, _, ok := readWorkerPidFile(path); ok {
		t.Fatal("missing file should be ok=false")
	}
	writeWorkerPid(t, path, 321, 654)
	if pid, sp, ok := readWorkerPidFile(path); !ok || pid != 321 || sp != 654 {
		t.Fatalf("round-trip = (%d,%d,%v), want (321,654,true)", pid, sp, ok)
	}
	if err := os.WriteFile(path, []byte("321"), 0o644); err != nil {
		t.Fatal(err)
	}
	if pid, sp, ok := readWorkerPidFile(path); !ok || pid != 321 || sp != 0 {
		t.Fatalf("one-line = (%d,%d,%v), want (321,0,true)", pid, sp, ok)
	}
}
