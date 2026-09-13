package worker_test

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/worker"
	wire "cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// A synthetic adapter explicitly reports fixture usage, then waits until the
// test has an unacknowledged accounting frame before completing inference.
type accountingGatedProvider struct {
	*fixedProvider
	release <-chan struct{}
}

func (p *accountingGatedProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	a := usage.StartAttempt(ctx, p.Name(), "fake-model")
	a.Observe(llm.TokenUsage{Input: llm.ReportedTokens(11), Output: llm.ReportedTokens(7)}, nil)
	select {
	case <-p.release:
	case <-ctx.Done():
		a.Finish(usage.Interrupted)
		return nil, ctx.Err()
	}
	r, err := p.fixedProvider.StreamChat(ctx, req)
	if err != nil {
		a.Finish(usage.Failed)
		return nil, err
	}
	return a.TrackStream(r), nil
}
func TestModelTurnCompletesBeforeAccountingReceipt(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseModel := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseModel()
	provider := &accountingGatedProvider{fixedProvider: &fixedProvider{text: "completed without accounting wait"}, release: release}
	service := &fakeResolver{prov: provider}
	tools := &fakeToolSvc{reg: agenttools.NewRegistry()}
	ws := worker.NewWithFactories(func(*wire.StartTurn) (providers.Resolver, error) { return service, nil }, func(*wire.StartTurn) (runner.ToolSvc, error) { return tools, nil })
	listener := bufconn.Listen(1 << 20)
	defer listener.Close()
	grpcServer := grpc.NewServer()
	wire.RegisterWorkerServer(grpcServer, ws)
	go grpcServer.Serve(listener)
	defer grpcServer.Stop()
	conn, err := grpc.NewClient("passthrough:///accounting-turn", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := wire.NewWorkerClient(conn)
	accountCtx, cancelAccount := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelAccount()
	account, err := client.Accounting(accountCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err = account.Send(&wire.WorkerAccountingReceipt{}); err != nil {
		t.Fatal(err)
	}
	if _, err = account.Recv(); err != nil {
		t.Fatal(err)
	}
	turnCtx, cancelTurn := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancelTurn()
	defer func() {
		releaseModel()
		cancelTurn()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = ws.CloseAccounting(ctx)
	}()
	turn, err := client.RunTurn(turnCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err = turn.Send(&wire.HostToWorker{Msg: &wire.HostToWorker_Start{Start: &wire.StartTurn{ConversationId: "conversation", Input: "hello", WorkDir: t.TempDir(), Config: &wire.ConfigSnapshot{LocusMode: "open_primary"}, Accounting: &wire.AccountingWork{OperationId: "parent-operation", SessionId: "parent-session", Source: "main"}}}}); err != nil {
		t.Fatal(err)
	}
	first, err := account.Recv()
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately do not acknowledge first. The accounting writer is waiting on
	// this receipt throughout ordinary model completion and turn EOF.
	releaseModel()
	var completed *wire.TurnDone
	for {
		m, err := turn.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e := m.GetError(); e != nil {
			t.Fatalf("turn error: %s", e.Message)
		}
		if d := m.GetDone(); d != nil {
			completed = d
		}
	}
	if completed == nil || completed.FinalText != "completed without accounting wait" {
		t.Fatalf("turn did not complete: %+v", completed)
	}
	finalObserved := false
	observe := func(b *wire.WorkerAccountingBatch) {
		for _, a := range b.Observations {
			if a.Outcome == "completed" {
				finalObserved = true
				if a.OperationId != "parent-operation" || a.SessionId != "parent-session" || a.ConversationId != "conversation" || a.InputTokens == nil || *a.InputTokens != 11 || a.OutputTokens == nil || *a.OutputTokens != 7 {
					t.Fatalf("final accounting=%+v", a)
				}
			}
		}
	}
	observe(first)
	if err = account.Send(&wire.WorkerAccountingReceipt{BatchId: first.BatchId, Persisted: true}); err != nil {
		t.Fatal(err)
	}
	if err = account.Send(&wire.WorkerAccountingReceipt{Drain: true}); err != nil {
		t.Fatal(err)
	}
	for {
		batch, err := account.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if batch.DrainFinished {
			if batch.DrainError != "" {
				t.Fatal(batch.DrainError)
			}
			break
		}
		observe(batch)
		if err = account.Send(&wire.WorkerAccountingReceipt{BatchId: batch.BatchId, Persisted: true}); err != nil {
			t.Fatal(err)
		}
	}
	if !finalObserved {
		t.Fatal("completed physical attempt not delivered after turn EOF")
	}
}
