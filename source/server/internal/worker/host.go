package worker

// host.go — host-side workerRunner, implementing runner.TurnRunner.
//
// workerRunner.RunTurn:
//  1. Pre-assembles history + project context from the host's persistence layer.
//  2. Spawns a "cercano worker" child via spawnWorker.
//  3. Opens the bidi gRPC stream and sends StartTurn.
//  4. DRAIN loop: routes WorkerToHost messages to the caller's callbacks.
//     - WorkerEvent     → sink.Emit
//     - PermissionRequest → requester (goroutine) → PermissionResponse
//     - CredentialRequest  → resolve on host (keychain/OAuth) → CredentialResponse
//     - PersistTurn       → persist(msg)
//     - TurnDone          → capture Result, return nil
//     - TurnError         → return error
//  5. Worker crash (stream EOF/error without TurnDone) → Kill + codes.Unavailable.
//  6. ctx cancel → send Cancel, Kill, return ctx.Err().
//
// Concurrency: exactly one goroutine calls stream.Recv (the drain loop).
// Multiple goroutines may call stream.Send (permission + credential responses,
// Cancel) — protected by sendMu.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	pkgcfg "cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"
)

// dialFunc creates a gRPC connection to a worker. Injectable for tests.
type dialFunc func(ctx context.Context) (*grpc.ClientConn, error)

// workerRunner implements runner.TurnRunner by running turns in a child process.
type workerRunner struct {
	persist runner.TurnHistory
	cfg     cfgsvc.Service
	perms   permissions.Broker
	secrets secrets.Store

	// dial is called instead of the pool when non-nil (test injection). When
	// nil, RunTurn acquires a warm worker from the per-conversation pool.
	dial dialFunc

	// pool keeps one warm worker per conversation across turns (production
	// path). nil on the dial-injected test constructors, which reuse the
	// injected transport instead.
	pool *workerPool
}

// NewWorkerRunner builds a workerRunner that satisfies runner.TurnRunner.
// The caller supplies the host-side services the runner needs to:
//   - pre-assemble history + project context (persist),
//   - build the ConfigSnapshot + permission mode (cfg, perms),
//   - answer CredentialRequests from the worker (cfg + st).
func NewWorkerRunner(
	persist runner.TurnHistory,
	cfg cfgsvc.Service,
	perms permissions.Broker,
	st secrets.Store,
) runner.TurnRunner {
	pool := newWorkerPool(nil) // production: spawn via spawnWorker
	// Start the idle-reaper with the configured window. The reaper runs on a
	// background context and stops when the pool is Shut down (Shutdown closes
	// p.done). A window <= 0 (config's "disabled" sentinel) starts no goroutine.
	pool.StartReaper(context.Background(), cfg.Get().WorkerIdleTimeout())
	return &workerRunner{
		persist: persist,
		cfg:     cfg,
		perms:   perms,
		secrets: st,
		pool:    pool,
	}
}

// Shutdown drains the per-conversation worker pool: kills every warm worker and
// stops the idle-reaper. Safe to call once at host shutdown; a no-op on a
// dial-injected (test) runner with no pool.
func (w *workerRunner) Shutdown() {
	if w.pool != nil {
		w.pool.Shutdown()
	}
}

// DialFunc creates a gRPC connection to a worker. It is the injectable seam
// used to point a workerRunner at an in-process (bufconn) WorkerServer instead
// of a spawned process — see NewWorkerRunnerWithDial.
type DialFunc = dialFunc

// NewWorkerRunnerWithDial builds a workerRunner that dials an already-running
// WorkerServer via the supplied dial function instead of spawning a child
// process. This is the seam the server-level both-modes and crash-isolation
// tests use to drive the real host turn handler through a deterministic
// in-process (bufconn) worker. Production uses NewWorkerRunner (spawns).
func NewWorkerRunnerWithDial(
	persist runner.TurnHistory,
	cfg cfgsvc.Service,
	perms permissions.Broker,
	st secrets.Store,
	dial DialFunc,
) runner.TurnRunner {
	return newWorkerRunnerWithDial(persist, cfg, perms, st, dial)
}

// newWorkerRunnerWithDial builds a workerRunner with an injected dial function
// for in-process (bufconn) testing.
func newWorkerRunnerWithDial(
	persist runner.TurnHistory,
	cfg cfgsvc.Service,
	perms permissions.Broker,
	st secrets.Store,
	dial dialFunc,
) *workerRunner {
	return &workerRunner{
		persist: persist,
		cfg:     cfg,
		perms:   perms,
		secrets: st,
		dial:    dial,
	}
}

// RunTurn implements runner.TurnRunner.
func (w *workerRunner) RunTurn(
	ctx context.Context,
	req runner.Request,
	sink runner.EventSink,
	requester runner.PermissionRequester,
	persist runner.PersistFunc,
) (runner.Result, error) {
	// ── 1. Pre-assemble history + project context ──────────────────────────
	history := []llm.Message(nil)
	if w.persist != nil {
		history = w.persist.AssembleHistory(ctx, req.ConversationID)
	}
	projectCtx := ""
	if w.persist != nil {
		projectCtx = w.persist.LoadProjectContext(req.WorkDir)
	}

	// ── 2. Marshal history for the wire ───────────────────────────────────
	historyProto := make([]*proto.LLMMessage, 0, len(history))
	for _, m := range history {
		pm, err := MarshalMessage(m)
		if err != nil {
			return runner.Result{}, fmt.Errorf("workerRunner: marshal history: %w", err)
		}
		historyProto = append(historyProto, pm)
	}

	// ── 3. Build ConfigSnapshot + permission mode ──────────────────────────
	cfg := w.cfg.Get()
	snap := SnapshotConfig(cfg, "") // no credential in snapshot — worker fetches on demand
	permMode := string(agent.ModePermissive)
	if w.perms != nil {
		permMode = string(w.perms.Mode())
	}

	// ── 4. Build proto images ──────────────────────────────────────────────
	protoImages := make([]*proto.InlineImage, 0, len(req.Images))
	for _, img := range req.Images {
		protoImages = append(protoImages, &proto.InlineImage{
			Index:     int32(img.Index),
			Data:      img.Data,
			MediaType: img.MediaType,
		})
	}

	startTurn := &proto.StartTurn{
		ConversationId: req.ConversationID,
		Input:          req.Input,
		Images:         protoImages,
		WorkDir:        req.WorkDir,
		Gen:            req.Gen,
		Config:         snap,
		History:        historyProto,
		ProjectContext: projectCtx,
		PermissionMode: permMode,
	}

	// ── 5. Acquire a warm worker (pool) or use injected dial ──────────────
	//
	// turnHealthy records whether the worker is safe to keep WARM for the next
	// turn. It flips false on crash / ctx-cancel so the deferred cleanup evicts
	// + kills the worker (see the drain loop below).
	//   - dial path (tests): cleanup always closes the injected conn.
	//   - pool path (production): cleanup calls Release(convID, turnHealthy) —
	//     healthy keeps the process warm (conn NOT closed, owned by the pooled
	//     handle); unhealthy kills + evicts so the next Acquire spawns fresh.
	var (
		conn        *grpc.ClientConn
		cleanupFn   func()
		turnHealthy bool
	)

	if w.dial != nil {
		// Test path: use injected dial.
		c, err := w.dial(ctx)
		if err != nil {
			return runner.Result{}, fmt.Errorf("workerRunner: dial: %w", err)
		}
		conn = c
		cleanupFn = func() { _ = conn.Close() }
	} else {
		// Production path: acquire a warm worker from the per-conversation pool.
		wh, err := w.pool.Acquire(ctx, req.ConversationID, req.Gen)
		if err != nil {
			return runner.Result{}, fmt.Errorf("workerRunner: acquire: %w", err)
		}
		conn = wh.conn
		cleanupFn = func() { w.pool.Release(req.ConversationID, wh, turnHealthy) }
	}
	defer cleanupFn()

	// ── 6. Open bidi stream and send StartTurn ────────────────────────────
	client := proto.NewWorkerClient(conn)
	stream, err := client.RunTurn(ctx)
	if err != nil {
		return runner.Result{}, fmt.Errorf("workerRunner: open stream: %w", err)
	}

	if err := stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_Start{Start: startTurn}}); err != nil {
		return runner.Result{}, fmt.Errorf("workerRunner: send StartTurn: %w", err)
	}

	// sendMu serialises stream.Send calls from multiple goroutines (permission
	// and credential responses may fire concurrently from spawned goroutines).
	var sendMu sync.Mutex
	safeSend := func(msg *proto.HostToWorker) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(msg)
	}

	// ── 7. Drain loop ─────────────────────────────────────────────────────
	var turnDone bool
	var result runner.Result

	for {
		// Check for context cancellation before blocking on Recv.
		select {
		case <-ctx.Done():
			// Supersession / cancel: tell the worker to stop, then let the
			// deferred cleanup evict + kill it (turnHealthy stays false). A
			// canceled turn may leave the worker mid-tool in an ambiguous
			// state, so B1 does NOT keep it warm — the next turn spawns fresh.
			_ = safeSend(&proto.HostToWorker{Msg: &proto.HostToWorker_Cancel{Cancel: &proto.Cancel{}}})
			return runner.Result{}, ctx.Err()
		default:
		}

		msg, recvErr := stream.Recv()
		if recvErr != nil {
			if turnDone {
				// TurnDone received; the stream closed normally afterwards. The
				// worker finished cleanly → keep it WARM for the next turn.
				turnHealthy = true
				return result, nil
			}
			// Worker crashed or the stream died before TurnDone.
			// Kill is already deferred via cleanupFn; return Unavailable.
			if errors.Is(recvErr, context.Canceled) || errors.Is(recvErr, context.DeadlineExceeded) {
				return runner.Result{}, recvErr
			}
			return runner.Result{}, status.Errorf(codes.Unavailable, "worker turn failed: %v", recvErr)
		}

		switch m := msg.Msg.(type) {
		case *proto.WorkerToHost_Event:
			// Forward to the host's event sink.
			if sink != nil && m.Event != nil {
				sink.Emit(UnmarshalEvent(m.Event))
			}

		case *proto.WorkerToHost_PermRequest:
			// Answer in a goroutine so a slow human decision doesn't block Recv.
			pr := m.PermRequest
			go func() {
				id := pr.GetId()
				tier := llm.Permission(pr.GetTier())
				var args json.RawMessage
				if aj := pr.GetArgsJson(); aj != "" {
					args = json.RawMessage(aj)
				}
				allow, err := requester(ctx, pr.GetToolUseId(), pr.GetName(), args, tier, pr.GetDestructive())
				resp := &proto.PermissionResponse{
					Id:    id,
					Allow: allow,
				}
				if err != nil {
					resp.Error = err.Error()
				}
				if sendErr := safeSend(&proto.HostToWorker{Msg: &proto.HostToWorker_PermResponse{PermResponse: resp}}); sendErr != nil {
					log.Printf("[workerRunner] send perm response id=%d: %v", id, sendErr)
				}
			}()

		case *proto.WorkerToHost_CredRequest:
			// Answer credential requests off the drain path (in a goroutine) so
			// a slow keychain/OAuth resolve doesn't stall the event stream.
			cr := m.CredRequest
			go func() {
				id := cr.GetId()
				profileName := cr.GetProfileName()
				token, account, credErr := w.resolveCredential(ctx, cfg, profileName)
				resp := &proto.CredentialResponse{Id: id}
				if credErr != nil {
					resp.Error = credErr.Error()
				} else {
					resp.Token = token
					resp.Account = account
				}
				if sendErr := safeSend(&proto.HostToWorker{Msg: &proto.HostToWorker_CredResponse{CredResponse: resp}}); sendErr != nil {
					log.Printf("[workerRunner] send cred response id=%d: %v", id, sendErr)
				}
			}()

		case *proto.WorkerToHost_Persist:
			// Forward persist to the host's fenced persist callback.
			if persist != nil && m.Persist != nil && m.Persist.Message != nil {
				lm, err := UnmarshalMessage(m.Persist.Message)
				if err != nil {
					log.Printf("[workerRunner] unmarshal persist message: %v", err)
				} else {
					persist(lm)
				}
			}

		case *proto.WorkerToHost_Done:
			// Capture result; continue draining until Recv returns EOF.
			d := m.Done
			result = runner.Result{
				FinalText:    d.GetFinalText(),
				Model:        d.GetModel(),
				IsCloud:      d.GetIsCloud(),
				InputTokens:  int(d.GetInputTokens()),
				OutputTokens: int(d.GetOutputTokens()),
				Notice:       d.GetNotice(),
			}
			turnDone = true

		case *proto.WorkerToHost_Error:
			msg := "worker turn error"
			if m.Error != nil {
				msg = m.Error.GetMessage()
			}
			return runner.Result{}, errors.New(msg)
		}
	}
}

// resolveCredential answers a CredentialRequest from the worker by reading
// the actual credential from the host's keychain/secrets store, mirroring
// the logic in internal/hostsvc/providers/providers.go:rebuildCloud.
func (w *workerRunner) resolveCredential(ctx context.Context, cfg pkgcfg.Config, profileName string) (token, account string, err error) {
	// Find the profile.
	var prof pkgcfg.CloudProfile
	found := false
	for _, p := range cfg.CloudProfiles {
		if p.Name == profileName {
			prof = p
			found = true
			break
		}
	}
	if !found {
		return "", "", fmt.Errorf("credential: profile %q not found", profileName)
	}

	st := w.secrets
	if st == nil {
		return "", "", fmt.Errorf("credential: secrets store not configured")
	}

	// ChatGPT subscription: call the token source to get a fresh access token.
	if prof.Flavor == cloudfactory.FlavorResponses && prof.Route == cloudfactory.RouteChatGPT {
		src := chatgptauth.NewSource(st, profileName, chatgptauth.Flow{})
		access, accountID, err := src.Token(ctx)
		if err != nil {
			return "", "", fmt.Errorf("credential: chatgpt token: %w", err)
		}
		return access, accountID, nil
	}

	// Static-key route: read the API key from the secrets store.
	key, err := st.Get(profileName)
	if err != nil {
		return "", "", fmt.Errorf("credential: get key for profile %q: %w", profileName, err)
	}
	return key, "", nil
}

// testDialUnix returns a dialFunc that dials a bufconn listener via the given
// net.Listener (used in unit tests).
func testDialUnix(lis interface {
	DialContext(ctx context.Context) (net.Conn, error)
}) dialFunc {
	return func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallSendMsgSize(maxGRPCWorkerMsgBytes),
				grpc.MaxCallRecvMsgSize(maxGRPCWorkerMsgBytes),
			),
		)
	}
}
