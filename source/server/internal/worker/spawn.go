package worker

// spawn.go — host-side worker process lifecycle.
//
// spawnWorker starts a "cercano worker --socket <path>" child process,
// polls until its unix-socket listener is ready, and gRPC-dials it.
// workerHandle.Kill() shuts it down cleanly regardless of how it died.
//
// Process-group and pidfile patterns mirror internal/meridian/manager.go.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// cercanoBinaryName is the sibling server binary's filename: on Windows it
// carries the required .exe extension; elsewhere it's extension-less.
var cercanoBinaryName = func() string {
	if runtime.GOOS == "windows" {
		return "cercano.exe"
	}
	return "cercano"
}()

const (
	// workerSocketDir is the directory where per-turn unix sockets live.
	workerSocketDir = "cercano-workers"

	// spawnReadyTimeout is the maximum time to wait for the worker to bind
	// its unix socket. Workers start fast (no heavy init); 10s is generous.
	spawnReadyTimeout = 10 * time.Second

	// spawnPollInterval is how often we check whether the worker is ready.
	spawnPollInterval = 10 * time.Millisecond

	maxGRPCWorkerMsgBytes = 64 * 1024 * 1024 // 64 MiB — matches agentclient
)

// MaxMsgBytes is the host↔worker gRPC message-size limit, exported so the
// worker's gRPC server (cmd/cercano) can set the SAME limit its client dials
// with. Host and worker must agree, or large StartTurn / result messages fail
// with ResourceExhausted on the smaller side.
const MaxMsgBytes = maxGRPCWorkerMsgBytes

// workerHandle owns a spawned worker process and its gRPC connection.
type workerHandle struct {
	cmd        *exec.Cmd
	conn       *grpc.ClientConn
	socketPath string
	pidPath    string
	// onKill, when set, is invoked by Kill after the process/conn teardown. It is
	// the injectable kill seam: production leaves it nil; tests set it to observe
	// that a specific handle was reaped (a dial/bufconn handle has no OS process
	// to signal, so signalling alone can't prove a reap).
	onKill func()
}

// Kill shuts the worker down: kills its process group, closes the gRPC
// connection, and removes the socket + pidfile. Idempotent.
func (h *workerHandle) Kill() {
	if h == nil {
		return
	}
	if h.cmd != nil && h.cmd.Process != nil {
		killGroupOrProcess(h.cmd.Process)
		// Best-effort wait so the OS reclaims the zombie.
		_ = h.cmd.Wait()
		// Only our own pidfile is removed; needs the pid, so it lives inside
		// this guard (a partially-built handle has no process and no pidfile).
		removePidFileIfOwned(h.pidPath, h.cmd.Process.Pid)
	}
	if h.conn != nil {
		_ = h.conn.Close()
	}
	if h.socketPath != "" {
		_ = os.Remove(h.socketPath)
	}
	if h.onKill != nil {
		h.onKill()
	}
}

// spawnWorker starts a worker process on a fresh unix socket and returns a
// connected handle. The caller must call Kill() when done.
func spawnWorker(ctx context.Context, conversationID string, gen uint64) (*workerHandle, error) {
	bin, err := findWorkerBinary()
	if err != nil {
		return nil, fmt.Errorf("worker spawn: %w", err)
	}

	sockPath, pidPath, err := workerPaths(conversationID, gen)
	if err != nil {
		return nil, fmt.Errorf("worker spawn: paths: %w", err)
	}

	// Remove any stale socket from a previous run (shouldn't happen, but safe).
	_ = os.Remove(sockPath)

	// Redirect stdout/stderr to the logger for crash diagnosis.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("worker spawn: pipe: %w", err)
	}

	// Deliberately exec.Command, NOT exec.CommandContext(ctx, …): the worker
	// process must OUTLIVE the turn that spawned it. The pool keeps it warm across
	// turns; binding its lifetime to the turn ctx would SIGKILL it the instant the
	// first turn completes (the turn ctx cancels on completion), silently defeating
	// warm reuse — every turn would respawn. The pool owns teardown instead:
	// Kill() / Shutdown() / idle-reap / startup orphan-sweep. The turn ctx still
	// bounds STARTUP via waitForSocket(ctx, …) below, so a hung spawn during this
	// turn is still abortable (and the partial handle is Killed on that path).
	cmd := exec.Command(bin, "worker", "--socket", sockPath)
	setProcessGroup(cmd)
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return nil, fmt.Errorf("worker spawn: start %s: %w", bin, err)
	}
	_ = pw.Close() // parent's copy; child inherits it

	// Tee worker output to the log in the background.
	go func() {
		data, _ := io.ReadAll(pr)
		_ = pr.Close()
		if len(data) > 0 {
			log.Printf("[worker pid=%d] %s", cmd.Process.Pid, data)
		}
	}()

	// Write pidfile so a future host startup can reap orphans (Phase 6).
	if pidPath != "" {
		if err := writePidFile(pidPath, cmd.Process.Pid, os.Getpid()); err != nil {
			log.Printf("[worker] cannot write pidfile %s: %v", pidPath, err)
		}
	}

	h := &workerHandle{
		cmd:        cmd,
		socketPath: sockPath,
		pidPath:    pidPath,
	}

	// Poll until the worker's unix socket accepts connections.
	if err := waitForSocket(ctx, sockPath, spawnReadyTimeout); err != nil {
		h.Kill()
		return nil, fmt.Errorf("worker spawn: socket %s not ready: %w", sockPath, err)
	}

	// gRPC-dial the unix socket (insecure, same machine / same user).
	// The single-colon "unix:" form (not "unix://") is required for a Windows
	// path: "unix://C:\..." makes gRPC's target parser treat "C:\..." as a
	// host, and the drive-letter colon then breaks host:port splitting
	// ("too many colons in address"). "unix:" avoids the authority parse
	// entirely and works for POSIX absolute paths too.
	conn, err := grpc.NewClient(
		"unix:"+sockPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(maxGRPCWorkerMsgBytes),
			grpc.MaxCallRecvMsgSize(maxGRPCWorkerMsgBytes),
		),
	)
	if err != nil {
		h.Kill()
		return nil, fmt.Errorf("worker spawn: grpc dial %s: %w", sockPath, err)
	}
	h.conn = conn
	return h, nil
}

// findWorkerBinary locates the cercano binary, mirroring agentclient.findCercanoBinary.
func findWorkerBinary() (string, error) {
	// 1. Sibling to this running binary.
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), cercanoBinaryName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	// 2. $PATH.
	if p, err := exec.LookPath("cercano"); err == nil {
		return p, nil
	}
	return "", errors.New("worker: `cercano` binary not found (looked next to self and in $PATH)")
}

// workerPaths returns (socketPath, pidPath, error) for this turn's worker.
func workerPaths(conversationID string, gen uint64) (string, string, error) {
	dir := filepath.Join(os.TempDir(), workerSocketDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tag := fmt.Sprintf("%s-%d-%08x", sanitizeID(conversationID), gen, rand.Uint32())
	sockPath := filepath.Join(dir, tag+".sock")
	pidPath := filepath.Join(dir, tag+".pid")
	return sockPath, pidPath, nil
}

// sanitizeID truncates/cleans a conversation ID for use in a file name.
func sanitizeID(id string) string {
	// Keep first 16 chars; replace non-alphanumeric chars.
	const max = 16
	runes := []rune(id)
	if len(runes) > max {
		runes = runes[:max]
	}
	for i, r := range runes {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			runes[i] = '_'
		}
	}
	if len(runes) == 0 {
		return "worker"
	}
	return string(runes)
}

// waitForSocket polls until the given unix socket path accepts a connection,
// or until timeout / ctx cancel.
func waitForSocket(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("socket did not become available within %s", timeout)
		}
		time.Sleep(spawnPollInterval)
	}
}

// ─── pidfile helpers (mirror meridian/manager.go) ─────────────────────────────

// writePidFile records the worker's pid (= pgid) and the spawner's pid.
func writePidFile(path string, pid, spawnerPid int) error {
	if path == "" {
		return nil
	}
	data := strconv.Itoa(pid) + "\n" + strconv.Itoa(spawnerPid) + "\n"
	return os.WriteFile(path, []byte(data), 0o644)
}

// removePidFileIfOwned deletes the pidfile only when it still names pid.
func removePidFileIfOwned(path string, pid int) {
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := splitLines(string(data))
	if len(lines) == 0 {
		return
	}
	recorded, err := strconv.Atoi(lines[0])
	if err != nil || recorded != pid {
		return
	}
	_ = os.Remove(path)
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(s) && s[start:] != "" {
		out = append(out, s[start:])
	}
	return out
}
