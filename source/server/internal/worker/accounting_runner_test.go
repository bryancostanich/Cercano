package worker_test

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	wire "cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type captureAccountingReceiver struct {
	*telemetry.Collector
	mu  sync.Mutex
	ids map[string]bool
}

func (c *captureAccountingReceiver) TryAttemptBatch(observations []usage.AttemptObservation) (<-chan bool, error) {
	receipt, err := c.Collector.TryAttemptBatch(observations)
	if err == nil {
		c.mu.Lock()
		for _, a := range observations {
			c.ids[a.ID] = true
		}
		c.mu.Unlock()
	}
	return receipt, err
}

type initiallyUnavailableAccounting struct {
	*worker.WorkerServer
	calls atomic.Int32
}

func (s *initiallyUnavailableAccounting) Accounting(stream wire.Worker_AccountingServer) error {
	if s.calls.Add(1) == 1 {
		return status.Error(codes.Unavailable, "synthetic startup disconnect")
	}
	return s.WorkerServer.Accounting(stream)
}

func TestWorkerRunnerOwnsAccountingAcrossTurnsAndReconnect(t *testing.T) {
	gate := make(chan struct{})
	close(gate)
	provider := &accountingGatedProvider{fixedProvider: &fixedProvider{text: "accounted"}, release: gate}
	resolver := &fakeResolver{prov: provider}
	tools := &fakeToolSvc{reg: agenttools.NewRegistry()}
	ws := worker.NewWithFactories(func(*wire.StartTurn) (providers.Resolver, error) { return resolver, nil }, func(*wire.StartTurn) (runner.ToolSvc, error) { return tools, nil })
	service := &initiallyUnavailableAccounting{WorkerServer: ws}
	listener := bufconn.Listen(1 << 20)
	defer listener.Close()
	grpcServer := grpc.NewServer()
	wire.RegisterWorkerServer(grpcServer, service)
	go grpcServer.Serve(listener)
	defer grpcServer.Stop()
	store, err := telemetry.NewSQLiteStore(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector := telemetry.NewCollector(store, 8)
	defer collector.Close()
	collector.SetSessionID("agent-session")
	if err = collector.EnableAccounting(telemetry.AccountingOptions{Capacity: 32, FlushInterval: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	receiver := &captureAccountingReceiver{Collector: collector, ids: make(map[string]bool)}
	var dials atomic.Int32
	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		dials.Add(1)
		return grpc.NewClient("passthrough:///accounting-runner", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	cfg := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	turnRunner := worker.NewWorkerRunnerWithDial(nil, cfg, newTestBroker(), dial)
	if err = turnRunner.(worker.AccountingConfigurer).SetAccountingReceiver(receiver); err != nil {
		t.Fatal(err)
	}
	defer turnRunner.(interface{ Shutdown() }).Shutdown()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		ctx = usage.WithAttempts(ctx, collector.EmitAttempt, usage.Attribution{OperationID: fmt.Sprintf("operation-%d", i), SessionID: "parent-session", Source: "main"})
		result, err := turnRunner.RunTurn(ctx, runner.Request{ConversationID: "conversation", Input: "hello", WorkDir: t.TempDir(), Gen: uint64(i + 1)}, &sinkCollector{}, nil, nil)
		cancel()
		if err != nil || result.FinalText != "accounted" {
			t.Fatalf("turn %d: %+v %v", i, result, err)
		}
	}
	// Drain on runner shutdown, not on either normal turn's completion.
	turnRunner.(interface{ Shutdown() }).Shutdown()
	if dials.Load() != 1 {
		t.Fatalf("accounting lifetime redialed per turn: %d", dials.Load())
	}
	if service.calls.Load() < 2 {
		t.Fatal("accounting connection did not recover")
	}
	receiver.mu.Lock()
	ids := make([]string, 0, len(receiver.ids))
	for id := range receiver.ids {
		ids = append(ids, id)
	}
	receiver.mu.Unlock()
	if len(ids) != 2 {
		t.Fatalf("physical attempt identities=%v", ids)
	}
	operations := map[string]bool{}
	workerID := ""
	for _, id := range ids {
		a, err := store.AccountingAttempt(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if a.Outcome != usage.Completed || a.Tokens.Input.Value != 11 || a.Tokens.Output.Value != 7 || !a.Tokens.TotalsKnown() || a.Attribution.SessionID != "parent-session" || a.Attribution.ConversationID != "conversation" {
			t.Fatalf("persisted attempt=%+v", a)
		}
		operations[a.Attribution.OperationID] = true
		if workerID == "" {
			workerID = a.Attribution.WorkerID
		} else if workerID != a.Attribution.WorkerID {
			t.Fatal("warm worker identity changed across turns")
		}
	}
	if !operations["operation-0"] || !operations["operation-1"] || workerID == "" {
		t.Fatalf("operation/worker attribution=%v %q", operations, workerID)
	}
	if h := collector.AccountingHealth(); h.CoverageIncomplete || h.Lost != 0 || h.Uncertain != 0 {
		t.Fatalf("recovered connection lost records: %+v", h)
	}
}
