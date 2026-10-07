//go:build darwin && cgo

package brewrestart

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type restartFixture struct {
	proto.UnimplementedAgentServer
	stop chan struct{}
	once sync.Once
	busy bool
	root string
}

// ShutdownAgent is the LEGACY fire-and-forget bounce. The restart
// coordinator must never call it after the safe-stop migration, so the
// fixture records any call as a marker file for tests to fail on.
func (s *restartFixture) ShutdownAgent(context.Context, *proto.ShutdownAgentRequest) (*proto.ShutdownAgentResponse, error) {
	_ = os.WriteFile(filepath.Join(s.root, "legacy-called"), []byte("legacy ShutdownAgent called"), 0600)
	return &proto.ShutdownAgentResponse{Accepted: true}, nil
}

// ShutdownAgentWhenIdle mirrors the real server's identity guard: only the
// PID from a verified ownership inspection may commit the safe stop. Busy
// fixtures block until the request deadline, exactly like an agent with
// active update-relevant work — they never stop on their own.
func (s *restartFixture) ShutdownAgentWhenIdle(ctx context.Context, req *proto.ShutdownAgentWhenIdleRequest) (*proto.ShutdownAgentWhenIdleResponse, error) {
	if req.GetExpectedPid() != int64(os.Getpid()) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"expected_pid %d does not match this agent process (pid %d)", req.GetExpectedPid(), os.Getpid())
	}
	if s.busy {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "safe stop wait timed out")
	}
	_ = os.WriteFile(filepath.Join(s.root, "safe-stop-pid"), []byte(strconv.FormatInt(req.GetExpectedPid(), 10)), 0600)
	s.once.Do(func() { close(s.stop) })
	return &proto.ShutdownAgentWhenIdleResponse{Accepted: true, Message: "safe stop committed"}, nil
}

// oldRestartFixture models a released agent binary predating the safe-stop
// method: only the legacy RPC exists; ShutdownAgentWhenIdle answers
// codes.Unimplemented via the embedded server.
type oldRestartFixture struct {
	proto.UnimplementedAgentServer
	root string
}

func (s *oldRestartFixture) ShutdownAgent(context.Context, *proto.ShutdownAgentRequest) (*proto.ShutdownAgentResponse, error) {
	_ = os.WriteFile(filepath.Join(s.root, "legacy-called"), []byte("legacy ShutdownAgent called"), 0600)
	return &proto.ShutdownAgentResponse{Accepted: true}, nil
}

func TestMain(m *testing.M) {
	if os.Getenv("CERCANO_COORD_FIXTURE") == "1" && len(os.Args) == 2 && os.Args[1] == "agent" {
		os.Exit(runRestartFixture())
	}
	os.Exit(m.Run())
}

func runRestartFixture() int {
	root := os.Getenv("CERCANO_COORD_ROOT")
	exe, _ := os.Executable()
	version := filepath.Base(filepath.Dir(filepath.Dir(exe)))
	if version == "2" {
		data, _ := os.ReadFile(filepath.Join(root, "old-pid"))
		pid, _ := strconv.Atoi(string(data))
		if _, err := Inspect(pid); err == nil {
			_ = os.WriteFile(filepath.Join(root, "overlap"), []byte("old process still alive"), 0600)
		}
	}
	listener, err := net.Listen("tcp", os.Getenv("CERCANO_COORD_ENDPOINT"))
	if err != nil {
		return 2
	}
	gs := grpc.NewServer()
	var service *restartFixture
	switch mode := os.Getenv("CERCANO_COORD_FIXTURE_MODE"); mode {
	case "old":
		// A released agent binary predating the safe-stop RPC: only the
		// legacy bounce exists; ShutdownAgentWhenIdle answers Unimplemented.
		proto.RegisterAgentServer(gs, &oldRestartFixture{root: root})
	case "busy":
		// Active update-relevant work: the safe stop waits and never stops
		// the process; the fixture stays alive until the test kills it.
		service = &restartFixture{stop: make(chan struct{}), busy: true, root: root}
		proto.RegisterAgentServer(gs, service)
	default:
		service = &restartFixture{stop: make(chan struct{}), root: root}
		proto.RegisterAgentServer(gs, service)
	}
	go func() { _ = gs.Serve(listener) }()
	cwd, _ := os.Getwd()
	_ = os.WriteFile(filepath.Join(root, version+"-ready"), []byte(fmt.Sprintf("%d\n%s\n%s", os.Getpid(), cwd, os.Getenv("FIXTURE_SETTING"))), 0600)
	if service == nil {
		// old fixture: nothing can commit a stop; stay put.
		time.Sleep(20 * time.Second)
		return 3
	}
	if service.busy {
		// busy fixture: never commits; stay alive until killed.
		time.Sleep(20 * time.Second)
		return 3
	}
	select {
	case <-service.stop:
	case <-time.After(20 * time.Second):
		return 3
	}
	time.Sleep(50 * time.Millisecond) // allow accepting RPC response to flush
	gs.GracefulStop()
	_ = os.WriteFile(filepath.Join(root, version+"-port-closed"), []byte("closed"), 0600)
	time.Sleep(300 * time.Millisecond) // model cleanup after the listener closes
	return 0
}

func waitFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			return b
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture did not become ready")
	return nil
}

func TestNativeCoordinatorRestartsWithoutClients(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	oldExe := filepath.Join(root, "Cellar", "cercano", "1", "bin", "cercano")
	newExe := filepath.Join(root, "Cellar", "cercano", "2", "bin", "cercano")
	for _, path := range []string{oldExe, newExe} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0700); err != nil {
			t.Fatal(err)
		}
	}
	reserve, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := netip.MustParseAddrPort(reserve.Addr().String())
	_ = reserve.Close()
	old := exec.Command(oldExe, "agent")
	old.Dir = root
	old.Env = []string{"HOME=" + root, "TMPDIR=" + root, "CERCANO_COORD_FIXTURE=1", "CERCANO_COORD_ROOT=" + root, "CERCANO_COORD_ENDPOINT=" + endpoint.String(), "FIXTURE_SETTING=preserved value"}
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	oldDone := make(chan struct{})
	go func() { _ = old.Wait(); close(oldDone) }()
	t.Cleanup(func() { _ = old.Process.Kill(); <-oldDone })
	_ = os.WriteFile(filepath.Join(root, "old-pid"), []byte(strconv.Itoa(old.Process.Pid)), 0600)
	waitFixtureFile(t, filepath.Join(root, "1-ready"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ops := nativeRestartOps()
	ops.source = fixtureProcesses{pid: old.Process.Pid} // never enumerate/inspect the developer's processes
	// A reconnecting client's launcher must remain blocked until readiness.
	var readyConfirmed atomic.Bool
	contender := make(chan bool, 1)
	realShutdown := ops.shutdown
	ops.shutdown = func(ctx context.Context, id Identity, endpoint netip.AddrPort) error {
		err := realShutdown(ctx, id, endpoint)
		if err == nil {
			go func() {
				release, err := agentclient.AcquireAutoLaunchLock(ctx, root)
				if err != nil {
					contender <- false
					return
				}
				observed := readyConfirmed.Load()
				release()
				contender <- observed
			}()
		}
		return err
	}
	realReady := ops.ready
	ops.ready = func(ctx context.Context, id Identity, endpoint netip.AddrPort) error {
		err := realReady(ctx, id, endpoint)
		if err == nil {
			readyConfirmed.Store(true)
		}
		return err
	}
	realStart := ops.start
	var replacement Identity
	ops.start = func(path string, state LaunchState) (Identity, error) {
		id, err := realStart(path, state)
		replacement = id
		return id, err
	}
	t.Cleanup(func() {
		if replacement.PID > 0 {
			if current, err := Inspect(replacement.PID); err == nil && current.SameProcess(replacement) {
				p, _ := os.FindProcess(replacement.PID)
				_ = p.Kill()
			}
		}
	})
	started := time.Now()
	restarted, err := coordinateRestart(ctx, ops, newExe, uint32(os.Getuid()), endpoint)
	if err != nil || !restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	select {
	case ready := <-contender:
		if !ready {
			t.Fatal("reconnecting client acquired lock before readiness")
		}
	case <-ctx.Done():
		t.Fatal("reconnecting client remained blocked")
	}
	if time.Since(started) < 300*time.Millisecond {
		t.Fatal("replacement did not wait for old cleanup")
	}
	if _, err := os.Stat(filepath.Join(root, "overlap")); !os.IsNotExist(err) {
		t.Fatal("new process started before old exited")
	}
	// The update-related stop was the safe-stop RPC, committed exactly once
	// with the PID from the kernel-verified ownership inspection.
	stopPID, err := strconv.Atoi(strings.TrimSpace(string(waitFixtureFile(t, filepath.Join(root, "safe-stop-pid")))))
	if err != nil || stopPID != old.Process.Pid {
		t.Fatalf("safe stop committed for pid %d, want the verified old agent pid %d", stopPID, old.Process.Pid)
	}
	if _, err := os.Stat(filepath.Join(root, "legacy-called")); !os.IsNotExist(err) {
		t.Fatal("legacy ShutdownAgent was called instead of the safe-stop RPC")
	}
	ready := string(waitFixtureFile(t, filepath.Join(root, "2-ready")))
	want := fmt.Sprintf("%d\n%s\npreserved value", replacement.PID, root)
	if ready != want {
		t.Fatal("replacement launch settings were not preserved")
	}
	if replacement.Executable != newExe {
		t.Fatal("wrong executable started")
	}
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("old child not reaped")
	}
}
