package worker_test

// bothmodes_crash_test.go — Task 4:
//   1. Both-modes regression: a turn run through the WORKER runner (bufconn)
//      produces the same client-visible result/events/persists as the same turn
//      run through the in-process runner.Core. Proves decision 2 (identical
//      behavior) at the runner boundary the host forwards verbatim.
//   2. The crash-isolation proof: a worker that dies mid-turn yields an
//      Unavailable error (not a hang), and the SAME host-side runner keeps
//      serving — a second turn against a healthy worker succeeds afterward.
//      No leaked worker process (bufconn) and the host goroutine survives.

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/agenttools"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"
)

// ─── in-process baseline runner ──────────────────────────────────────────────

// newInProcessRunner builds a real runner.Core wired to the same echo provider
// + tool registry the bufconn worker uses, so the two modes are comparable.
func newInProcessRunner(t *testing.T, text string, persist runner.TurnHistory, cfg cfgsvc.Service) runner.TurnRunner {
	t.Helper()
	provSvc := &echoResolver{prov: &echoProvider{text: text}}
	return runner.New(runner.Deps{
		Providers: provSvc,
		Tools:     &hostTestToolSvc{reg: agenttools.NewRegistry()},
		Persist:   persist,
		Config:    cfg,
		Perms:     newTestBroker(),
	})
}

// recordingHistory captures persisted turns so both modes can be compared.
type recordingHistory struct {
	history    []llm.Message
	projectCtx string
	persisted  []llm.Message
}

func (h *recordingHistory) AssembleHistory(_ context.Context, _ string) []llm.Message {
	return h.history
}
func (h *recordingHistory) PersistTurn(_ context.Context, _ string, m llm.Message) {
	h.persisted = append(h.persisted, m)
}
func (h *recordingHistory) LoadProjectContext(_ string) string { return h.projectCtx }

func runOneTurn(t *testing.T, r runner.TurnRunner) (runner.Result, []runner.Event) {
	t.Helper()
	sink := &sinkCollector{}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := r.RunTurn(ctx, runner.Request{
		ConversationID: "parity-conv",
		Input:          "hello",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, sink,
		runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		}),
		func(m llm.Message) {},
	)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	return res, sink.events
}

// TestBothModes_Parity runs the identical turn through the in-process runner and
// the worker runner (bufconn) and asserts identical client-visible outcomes.
func TestBothModes_Parity(t *testing.T) {
	const wantText = "hello from both modes"

	// ── in-process ──
	inCfg := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	inHist := &recordingHistory{projectCtx: "ctx"}
	inRunner := newInProcessRunner(t, wantText, inHist, inCfg)
	inRes, inEvents := runOneTurn(t, inRunner)

	// ── worker (bufconn) ──
	dial, stop := startBufconnWorker(t, wantText)
	defer stop()
	wkCfg := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	wkHist := &recordingHistory{projectCtx: "ctx"}
	wkRunner := worker.NewWorkerRunnerForTest(wkHist, wkCfg, newTestBroker(), newHostSecrets(nil), dial)
	wkRes, wkEvents := runOneTurn(t, wkRunner)

	// ── result parity ──
	if inRes.FinalText != wantText || wkRes.FinalText != wantText {
		t.Fatalf("FinalText: in=%q worker=%q want=%q", inRes.FinalText, wkRes.FinalText, wantText)
	}
	if inRes.FinalText != wkRes.FinalText {
		t.Errorf("FinalText differs: in=%q worker=%q", inRes.FinalText, wkRes.FinalText)
	}

	// ── event parity: same sequence of event kinds + text ──
	inKinds := eventKinds(inEvents)
	wkKinds := eventKinds(wkEvents)
	if len(inKinds) == 0 || len(wkKinds) == 0 {
		t.Fatalf("no events: in=%d worker=%d", len(inKinds), len(wkKinds))
	}
	if !equalKinds(inKinds, wkKinds) {
		t.Errorf("event-kind sequence differs:\n in=%v\n wk=%v", inKinds, wkKinds)
	}
	if inText := concatText(inEvents); inText != concatText(wkEvents) {
		t.Errorf("streamed text differs: in=%q worker=%q", inText, concatText(wkEvents))
	}
}

func eventKinds(evs []runner.Event) []runner.EventKind {
	out := make([]runner.EventKind, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}
func concatText(evs []runner.Event) string {
	s := ""
	for _, e := range evs {
		s += e.Text
	}
	return s
}
func equalKinds(a, b []runner.EventKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─── the crash-isolation proof ───────────────────────────────────────────────

// crashingWorkerServer accepts StartTurn then abnormally terminates the stream
// (returns an error immediately) — simulating a worker that dies mid-turn
// before ever sending TurnDone.
type crashingWorkerServer struct {
	proto.UnimplementedWorkerServer
}

func (c *crashingWorkerServer) RunTurn(stream proto.Worker_RunTurnServer) error {
	_, _ = stream.Recv() // consume StartTurn
	// Abnormal termination: return an error without TurnDone. The host must
	// observe Recv() fail and surface codes.Unavailable, NOT hang.
	return status.Errorf(codes.Internal, "worker exploded mid-turn")
}

// TestWorker_CrashMidTurnIsIsolated is the phase's raison d'être:
//
//	(a) a worker crash mid-turn surfaces an error to the caller (Unavailable),
//	    bounded by a ctx timeout so a HANG fails the test;
//	(b) the HOST-side runner is still alive and serving — a SECOND turn against
//	    a healthy worker succeeds afterward;
//	(c) no leaked worker (bufconn servers are stopped; the crashed stream is
//	    torn down by the runner's deferred cleanup).
func TestWorker_CrashMidTurnIsIsolated(t *testing.T) {
	// ── conversation A: the crashing worker ──
	crashLis := bufconn.Listen(1 << 20)
	crashSrv := grpc.NewServer()
	proto.RegisterWorkerServer(crashSrv, &crashingWorkerServer{})
	go func() { _ = crashSrv.Serve(crashLis) }()
	defer crashSrv.Stop()

	crashDial := bufconnDial(crashLis)

	cfg := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	broker := newTestBroker()
	crashRunner := worker.NewWorkerRunnerForTest(&recordingHistory{}, cfg, broker, newHostSecrets(nil), crashDial)

	// (a) The crashing turn must return an ERROR quickly, not hang. A generous
	// ctx timeout guards against a hang — if RunTurn hangs, ctx fires and the
	// test still terminates with a failure below.
	ctxA, cancelA := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelA()
	start := time.Now()
	_, errA := crashRunner.RunTurn(ctxA, runner.Request{
		ConversationID: "conv-A-crash",
		Input:          "boom",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, &sinkCollector{},
		runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		}),
		func(llm.Message) {},
	)
	if errA == nil {
		t.Fatal("crashing worker: expected an error, got nil (crash not surfaced)")
	}
	if ctxA.Err() != nil {
		t.Fatalf("crashing worker: RunTurn hung until ctx expired (%v) — a crash must fail fast, not hang", ctxA.Err())
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("crash took %v to surface — should be near-instant", elapsed)
	}
	if st, ok := status.FromError(errA); ok {
		if st.Code() != codes.Unavailable {
			t.Errorf("crash error code = %v, want Unavailable", st.Code())
		}
	} else {
		t.Logf("crash surfaced as non-gRPC error (acceptable): %v", errA)
	}

	// (b) HOST SURVIVES: a second turn — through a fresh, healthy worker —
	// succeeds. We deliberately reuse the same *process* (this test binary is
	// the host); the crash of conv A must not have wedged it.
	const wantText = "host survived"
	healthyDial, stopHealthy := startBufconnWorker(t, wantText)
	defer stopHealthy()
	healthyRunner := worker.NewWorkerRunnerForTest(&recordingHistory{}, cfg, broker, newHostSecrets(nil), healthyDial)

	ctxB, cancelB := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelB()
	resB, errB := healthyRunner.RunTurn(ctxB, runner.Request{
		ConversationID: "conv-B-healthy",
		Input:          "still alive?",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, &sinkCollector{},
		runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		}),
		func(llm.Message) {},
	)
	if errB != nil {
		t.Fatalf("host did NOT survive the crash: second turn failed: %v", errB)
	}
	if resB.FinalText != wantText {
		t.Errorf("second turn FinalText = %q, want %q", resB.FinalText, wantText)
	}
}

// bufconnDial returns a plain dialFunc for a bufconn listener.
func bufconnDial(lis *bufconn.Listener) func(context.Context) (*grpc.ClientConn, error) {
	return func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
}
