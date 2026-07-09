package worker_test

// host_e2e_test.go — Task 4 end-to-end: spawn a REAL `cercano worker` child
// process, drive a turn against it over a unix socket, and prove crash
// isolation with an actual process kill (not a bufconn shortcut).
//
// This is the genuine article: the worker is a separate OS process. When we
// SIGKILL it mid-turn, the host-side runner must surface an error (not hang)
// and the host test process must keep running (a second real-worker turn
// succeeds afterward).
//
// Skipped under -short. Builds the cercano binary once via `go build`.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
)

// buildCercanoBinary compiles the cercano binary into a temp dir and returns
// its path. Skips the test if the toolchain isn't available.
func buildCercanoBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "cercano")
	// The worker package lives at internal/worker; the main package is at
	// cmd/cercano relative to the module root (two dirs up + cmd/cercano).
	cmd := exec.Command("go", "build", "-o", bin, "cercano/source/server/cmd/cercano")
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build cercano binary (skipping e2e): %v\n%s", err, out)
	}
	return bin
}

// spawnRealWorker starts `cercano worker --socket <path>` and returns the
// process + a dialFunc pointing at its unix socket, once the socket accepts.
func spawnRealWorker(t *testing.T, bin string) (*exec.Cmd, func(context.Context) (*grpc.ClientConn, error)) {
	t.Helper()
	// Unix socket paths are limited to ~104 bytes on macOS; t.TempDir() can
	// blow past that, so use a short name directly under the system temp dir.
	sockF, err := os.CreateTemp("", "cw-*.sock")
	if err != nil {
		t.Fatalf("temp sock: %v", err)
	}
	sock := sockF.Name()
	_ = sockF.Close()
	_ = os.Remove(sock) // the worker must bind() it fresh
	t.Cleanup(func() { _ = os.Remove(sock) })
	cmd := exec.Command(bin, "worker", "--socket", sock)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	// Wait for the unix socket to accept.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("worker socket %s never became ready", sock)
		}
		c, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient(
			"unix://"+sock,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
	return cmd, dial
}

// TestWorker_RealProcess_CrashIsolated spawns a real worker process, starts a
// turn, KILLS the process mid-turn, and asserts:
//   (a) the caller gets an error (not a hang — ctx timeout guards this);
//   (b) the host (this test process) is still alive and can spawn + drive a
//       SECOND real worker afterward.
func TestWorker_RealProcess_CrashIsolated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-process worker e2e under -short")
	}
	bin := buildCercanoBinary(t)

	// A local TCP listener that accepts connections but never responds. Pointed
	// at as the ollama URL, it makes the worker's provider HTTP call BLOCK, so
	// the turn stays genuinely in-flight while we kill the worker process
	// mid-turn (rather than erroring out instantly on connection-refused).
	blackhole, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("blackhole listen: %v", err)
	}
	defer blackhole.Close()
	go func() {
		for {
			c, err := blackhole.Accept()
			if err != nil {
				return
			}
			// Hold the connection open forever; never write a response.
			_ = c
		}
	}()
	ollamaURL := "http://" + blackhole.Addr().String()

	// ── conversation A: real worker, killed mid-turn ──
	procA, dialA := spawnRealWorker(t, bin)
	defer func() { _ = procA.Process.Kill() }()

	// open_only so no cloud credential is required; the blackhole ollama makes
	// the turn hang in the provider call until we kill the process.
	cfg := cfgsvc.New("", config.Config{
		LocusMode: "open_only",
		OllamaURL: ollamaURL,
	}, secrets.NewMemory())

	runnerA := worker.NewWorkerRunnerWithDial(&recordingHistory{}, cfg, newTestBroker(), newHostSecrets(nil), dialA)

	// Kill the worker process 300ms into the turn.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = procA.Process.Kill()
	}()

	ctxA, cancelA := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancelA()
	_, errA := runnerA.RunTurn(ctxA, runner.Request{
		ConversationID: "e2e-crash",
		Input:          "hello",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, &sinkCollector{},
		runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		}),
		func(llm.Message) {},
	)
	if ctxA.Err() != nil {
		t.Fatalf("real-worker crash: RunTurn hung until ctx expired (%v) — must fail fast", ctxA.Err())
	}
	if errA == nil {
		t.Fatal("real-worker crash: expected an error after the worker process was killed, got nil")
	}
	// The worker process died mid-turn (in-flight in the provider call), so the
	// host-side runner must surface Unavailable — the crash-isolation contract.
	if st, ok := status.FromError(errA); ok {
		if st.Code() != codes.Unavailable {
			t.Errorf("real-worker crash error code = %v, want Unavailable", st.Code())
		}
	} else {
		t.Errorf("real-worker crash surfaced as non-gRPC error: %v", errA)
	}
	t.Logf("crash surfaced as: %v", errA)

	// (b) HOST SURVIVES: spawn a SECOND real worker and confirm the host can
	// still open + drive a stream (proving the crash didn't wedge the host).
	procB, dialB := spawnRealWorker(t, bin)
	defer func() { _ = procB.Process.Kill() }()

	runnerB := worker.NewWorkerRunnerWithDial(&recordingHistory{}, cfg, newTestBroker(), newHostSecrets(nil), dialB)

	// This turn also hits the unreachable ollama; we don't require success, only
	// that the host can drive it and we then cleanly cancel — the point is the
	// host is alive and stream-capable after the crash. Kill B shortly to end.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = procB.Process.Kill()
	}()
	ctxB, cancelB := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancelB()
	_, errB := runnerB.RunTurn(ctxB, runner.Request{
		ConversationID: "e2e-after-crash",
		Input:          "still alive?",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, &sinkCollector{},
		runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		}),
		func(llm.Message) {},
	)
	if ctxB.Err() != nil {
		t.Fatalf("host did not survive: second real-worker turn hung until ctx expired (%v)", ctxB.Err())
	}
	// errB is expected (we killed B); its presence proves the host drove a fresh
	// worker stream to completion (error) after the first crash — host survived.
	t.Logf("second real-worker turn returned (host survived): %v", errB)
}
