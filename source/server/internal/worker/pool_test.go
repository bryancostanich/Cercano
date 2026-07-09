package worker_test

// pool_test.go — Task B1: per-conversation warm worker reuse.
//
// These tests drive the PRODUCTION pool path (dial == nil) via an injected
// spawn seam that returns bufconn-backed handles and counts spawns — so we
// exercise the real Acquire/Release warm/evict logic without spawning OS
// processes.
//
//   - two sequential turns on ONE conversation reuse ONE worker (spawn == 1);
//   - a crashed turn evicts, so the NEXT turn spawns fresh (spawn increments);
//   - a different conversation gets its own worker (spawn increments).

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/agenttools"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"
)

// bufconnWorkerConn starts an in-process (bufconn) WorkerServer that echoes and
// returns a connected gRPC conn plus a stop func. Reused across the pool tests.
func bufconnWorkerConn(t *testing.T, text string) (*grpc.ClientConn, func()) {
	t.Helper()
	prov := &echoProvider{text: text}
	provSvc := &echoResolver{prov: prov}
	ws := worker.NewWithFactories(
		func(_ *proto.StartTurn) (providers.Resolver, error) { return provSvc, nil },
		func(_ *proto.StartTurn) (runner.ToolSvc, error) {
			return &hostTestToolSvc{reg: agenttools.NewRegistry()}, nil
		},
	)
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	proto.RegisterWorkerServer(grpcSrv, ws)
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn worker: %v", err)
	}
	return conn, func() { _ = conn.Close(); grpcSrv.Stop() }
}

func runPoolTurn(t *testing.T, r runner.TurnRunner, convID string, gen uint64) (runner.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.RunTurn(ctx, runner.Request{
		ConversationID: convID,
		Input:          "hi",
		WorkDir:        t.TempDir(),
		Gen:            gen,
	}, &sinkCollector{},
		runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		}),
		func(llm.Message) {},
	)
}

// TestPool_ReuseSameConversation: two sequential turns on the same conversation
// reuse ONE worker (the spawn seam fires exactly once).
func TestPool_ReuseSameConversation(t *testing.T) {
	var spawns int32
	conn, stop := bufconnWorkerConn(t, "echo")
	defer stop()

	spawn := func(_ context.Context, _ string, _ uint64) (*worker.WorkerHandleForTest, error) {
		atomic.AddInt32(&spawns, 1)
		return worker.HandleFromConn(conn), nil
	}

	cfgSvc := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	r := worker.NewWorkerRunnerWithPoolSpawnForTest(&recordingHistory{}, cfgSvc, newTestBroker(), newHostSecrets(nil), spawn)

	if _, err := runPoolTurn(t, r, "conv-A", 1); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if _, err := runPoolTurn(t, r, "conv-A", 2); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	if got := atomic.LoadInt32(&spawns); got != 1 {
		t.Fatalf("two turns on one conversation should reuse ONE worker: spawns = %d, want 1", got)
	}
}

// TestPool_CrashEvictsThenRespawns: a crashed turn evicts the pooled worker, so
// the next turn on the SAME conversation spawns fresh.
func TestPool_CrashEvictsThenRespawns(t *testing.T) {
	var spawns int32

	// A worker that crashes: the WorkerServer stops before TurnDone, so the
	// host's Recv returns an error without a TurnDone → codes.Unavailable.
	makeCrashConn := func() (*grpc.ClientConn, func()) {
		lis := bufconn.Listen(1 << 20)
		grpcSrv := grpc.NewServer()
		// No WorkerServer registered → RunTurn RPC fails immediately (Unimplemented),
		// which the host surfaces as a crash (Unavailable) since no TurnDone arrives.
		go func() { _ = grpcSrv.Serve(lis) }()
		conn, err := grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("dial crash conn: %v", err)
		}
		return conn, func() { _ = conn.Close(); grpcSrv.Stop() }
	}

	healthyConn, healthyStop := bufconnWorkerConn(t, "echo")
	defer healthyStop()
	crashConn, crashStop := makeCrashConn()
	defer crashStop()

	spawn := func(_ context.Context, _ string, _ uint64) (*worker.WorkerHandleForTest, error) {
		n := atomic.AddInt32(&spawns, 1)
		if n == 1 {
			return worker.HandleFromConn(crashConn), nil // first turn crashes
		}
		return worker.HandleFromConn(healthyConn), nil // respawn is healthy
	}

	cfgSvc := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	r := worker.NewWorkerRunnerWithPoolSpawnForTest(&recordingHistory{}, cfgSvc, newTestBroker(), newHostSecrets(nil), spawn)

	// Turn 1 crashes → Unavailable, entry evicted.
	_, err := runPoolTurn(t, r, "conv-A", 1)
	if err == nil {
		t.Fatal("expected crash error on turn 1")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unavailable {
		t.Fatalf("turn 1 crash code = %v, want Unavailable", err)
	}

	// Turn 2 must spawn FRESH (crash evicted the pooled worker).
	if _, err := runPoolTurn(t, r, "conv-A", 2); err != nil {
		t.Fatalf("turn 2 (post-crash respawn): %v", err)
	}
	if got := atomic.LoadInt32(&spawns); got != 2 {
		t.Fatalf("crash should evict → next turn respawns: spawns = %d, want 2", got)
	}
}

// TestPool_DifferentConversationsGetOwnWorker: separate conversations do not
// share a worker.
func TestPool_DifferentConversationsGetOwnWorker(t *testing.T) {
	var spawns int32
	connA, stopA := bufconnWorkerConn(t, "echo")
	defer stopA()
	connB, stopB := bufconnWorkerConn(t, "echo")
	defer stopB()

	spawn := func(_ context.Context, convID string, _ uint64) (*worker.WorkerHandleForTest, error) {
		atomic.AddInt32(&spawns, 1)
		if convID == "conv-B" {
			return worker.HandleFromConn(connB), nil
		}
		return worker.HandleFromConn(connA), nil
	}

	cfgSvc := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	r := worker.NewWorkerRunnerWithPoolSpawnForTest(&recordingHistory{}, cfgSvc, newTestBroker(), newHostSecrets(nil), spawn)

	if _, err := runPoolTurn(t, r, "conv-A", 1); err != nil {
		t.Fatalf("conv-A: %v", err)
	}
	if _, err := runPoolTurn(t, r, "conv-B", 1); err != nil {
		t.Fatalf("conv-B: %v", err)
	}
	if got := atomic.LoadInt32(&spawns); got != 2 {
		t.Fatalf("two conversations should spawn two workers: spawns = %d, want 2", got)
	}
}
