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
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	"cercano/source/server/internal/inference"
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

	// openTierModel resolves the EFFECTIVE open model id for a tier on the
	// active runtime (override-else-catalog-default), so the ConfigSnapshot
	// carries host-resolved models the worker can use without the catalog. nil
	// on dial-injected test runners (they send no open tier models).
	openTierModel func(pkgcfg.Tier) string

	// srcMu guards the per-profile token-source caches below. Reusing one
	// Source per profile is what makes the sources' single-flight refresh
	// actually apply: a fresh Source per credential request gives each
	// concurrent caller its own mutex, and Anthropic/OpenAI refresh tokens
	// rotate (single-use), so racing refreshes invalidate each other.
	srcMu    sync.Mutex
	anthSrcs map[string]*anthropicauth.Source
	chatSrcs map[string]*chatgptauth.Source
	// anthFlow/chatFlow configure the token endpoints for the cached sources.
	// The zero value targets the real endpoints; tests override them.
	anthFlow anthropicauth.Flow
	chatFlow chatgptauth.Flow

	// openProvider returns the host's current open (local-runtime) inference.Provider,
	// used to answer a worker's OpenInferenceRequest. A factory (not a value) so
	// a host-side runtime/config swap is honored per request. nil on
	// dial-injected test runners without open support.
	openProvider func() inference.Provider

	// ensureSubagent creates a sub-agent conversation row when a worker-side
	// dispatch requests one over the stream. Wired from the server's store; nil
	// on dial-injected (test) runners (sub-agent rows are then not created).
	ensureSubagent EnsureSubagentFunc

	// dial is called instead of the pool when non-nil (test injection). When
	// nil, RunTurn acquires a warm worker from the per-conversation pool.
	dial dialFunc

	// pool keeps one warm worker per conversation across turns (production
	// path). nil on the dial-injected test constructors, which reuse the
	// injected transport instead.
	pool *workerPool
}

// EnsureSubagentFunc creates a sub-agent conversation row on the host when a
// worker-side dispatch asks for one over the stream. Wired from the server's
// conversation store.
type EnsureSubagentFunc func(ctx context.Context, id, parentID, projectDir, model string, grantedTools []string) error

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
	ensureSubagent EnsureSubagentFunc,
	openProvider func() inference.Provider,
	openTierModel func(pkgcfg.Tier) string,
) runner.TurnRunner {
	pool := newWorkerPool(nil) // production: spawn via spawnWorker
	// Start the idle-reaper with the configured window. The reaper runs on a
	// background context and stops when the pool is Shut down (Shutdown closes
	// p.done). A window <= 0 (config's "disabled" sentinel) starts no goroutine.
	pool.StartReaper(context.Background(), cfg.Get().WorkerIdleTimeout())
	return &workerRunner{
		persist:        persist,
		cfg:            cfg,
		perms:          perms,
		secrets:        st,
		ensureSubagent: ensureSubagent,
		openProvider:   openProvider,
		openTierModel:  openTierModel,
		pool:           pool,
	}
}

// resolveOpenTiers returns the effective open model id per tier for the active
// runtime, for the ConfigSnapshot. Empty when no resolver is wired (test
// runners) — the worker then simply has no open tier models.
func (w *workerRunner) resolveOpenTiers() map[string]string {
	if w.openTierModel == nil {
		return nil
	}
	out := map[string]string{}
	for _, t := range []pkgcfg.Tier{
		pkgcfg.TierMostCapable, pkgcfg.TierEveryday, pkgcfg.TierFastLight,
		pkgcfg.TierFastLightText, pkgcfg.TierEmbedding,
	} {
		if id := w.openTierModel(t); id != "" {
			out[string(t)] = id
		}
	}
	return out
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
	// Resolve the active runtime's effective open tier models host-side (the
	// worker cannot see the catalog); the snapshot carries them as overrides.
	snap := SnapshotConfig(cfg, "", w.resolveOpenTiers()) // no credential — worker fetches on demand
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
				var followUp *agent.FollowUpDenial
				if errors.As(err, &followUp) {
					// "Chat about this": relay the redirect message (not as an error) so
					// the worker rebuilds a FollowUpDenial and its tool loop continues.
					resp.Message = followUp.Message
				} else if err != nil {
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

		case *proto.WorkerToHost_OpenRequest:
			// Answer open-model inference off the drain path: the worker has no
			// local runtime manager, so it proxies open calls here and we run
			// them through the host's open provider, streaming events back.
			or := m.OpenRequest
			go w.serveOpenInference(ctx, or, safeSend)

		case *proto.WorkerToHost_Persist:
			if m.Persist != nil && m.Persist.Message != nil {
				lm, err := UnmarshalMessage(m.Persist.Message)
				if err != nil {
					log.Printf("[workerRunner] unmarshal persist message: %v", err)
				} else if cid := m.Persist.GetConversationId(); cid != "" {
					// Sub-agent turn: persist to its own sub-conversation. Best-effort
					// and unfenced — the sub-conversation is unique per dispatch, so
					// there is no supersession race the main-turn fence guards against.
					if w.persist != nil {
						w.persist.PersistTurn(ctx, cid, lm)
					}
				} else if persist != nil {
					persist(lm) // main conversation, fenced
				}
			}

		case *proto.WorkerToHost_EnsureSubagent:
			// A worker-side dispatch created a sub-agent: persist its conversation
			// row on the host so the tab survives restart and is post-mortemable.
			if w.ensureSubagent != nil && m.EnsureSubagent != nil {
				e := m.EnsureSubagent
				if err := w.ensureSubagent(ctx, e.GetId(), e.GetParentId(), e.GetProjectDir(), e.GetModel(), e.GetGrantedTools()); err != nil {
					log.Printf("[workerRunner] ensure subagent conversation: %v", err)
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
		access, accountID, err := w.chatgptSource(profileName).Token(ctx)
		if err != nil {
			return "", "", fmt.Errorf("credential: chatgpt token: %w", err)
		}
		return access, accountID, nil
	}

	// Claude subscription: same shape — call the token source for a fresh
	// bearer (no account id on the Anthropic path).
	if prof.Flavor == cloudfactory.FlavorMessages && prof.Route == cloudfactory.RouteSubscription {
		access, err := w.anthropicSource(profileName).Token(ctx)
		if err != nil {
			return "", "", fmt.Errorf("credential: claude subscription token: %w", err)
		}
		return access, "", nil
	}

	// Static-key route: read the API key from the secrets store.
	key, err := st.Get(profileName)
	if err != nil {
		return "", "", fmt.Errorf("credential: get key for profile %q: %w", profileName, err)
	}
	return key, "", nil
}

// anthropicSource returns the cached Anthropic token source for a profile,
// creating it on first use. Reusing one Source per profile keeps its
// single-flight refresh effective across concurrent credential requests.
func (w *workerRunner) anthropicSource(profile string) *anthropicauth.Source {
	w.srcMu.Lock()
	defer w.srcMu.Unlock()
	if s, ok := w.anthSrcs[profile]; ok {
		return s
	}
	if w.anthSrcs == nil {
		w.anthSrcs = make(map[string]*anthropicauth.Source)
	}
	s := anthropicauth.NewSource(w.secrets, profile, w.anthFlow)
	w.anthSrcs[profile] = s
	return s
}

// chatgptSource returns the cached ChatGPT token source for a profile,
// creating it on first use. See anthropicSource for why the instance is reused.
func (w *workerRunner) chatgptSource(profile string) *chatgptauth.Source {
	w.srcMu.Lock()
	defer w.srcMu.Unlock()
	if s, ok := w.chatSrcs[profile]; ok {
		return s
	}
	if w.chatSrcs == nil {
		w.chatSrcs = make(map[string]*chatgptauth.Source)
	}
	s := chatgptauth.NewSource(w.secrets, profile, w.chatFlow)
	w.chatSrcs[profile] = s
	return s
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

// serveOpenInference runs a proxied open-model call (OpenInferenceRequest) from
// the worker through the host's open provider and streams the events back as
// OpenInferenceEvents, terminating with done or a non-empty error. The worker
// has no local runtime manager, so this is how open_runtime=llama_server work
// runs in worker mode. Honors ctx cancel (a worker Cancel unwinds the stream).
func workerTemperatureForLog(t *float64) string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%g", *t)
}

func workerToolNamesForLog(tools []llm.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			names = append(names, tool.Name)
		}
	}
	return names
}

func workerTruncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func workerMessageShapesForLog(messages []llm.Message) string {
	parts := make([]string, 0, len(messages))
	for i, msg := range messages {
		blockParts := make([]string, 0, len(msg.Blocks))
		for _, block := range msg.Blocks {
			switch block.Type {
			case llm.BlockText:
				blockParts = append(blockParts, fmt.Sprintf("text:%q", workerTruncateRunes(strings.TrimSpace(block.Text), 80)))
			case llm.BlockToolUse:
				blockParts = append(blockParts, fmt.Sprintf("tool_use:%s", block.ToolName))
			case llm.BlockToolResult:
				blockParts = append(blockParts, fmt.Sprintf("tool_result:%s", block.ToolUseRef))
			default:
				blockParts = append(blockParts, string(block.Type))
			}
		}
		parts = append(parts, fmt.Sprintf("%d:%s[%s]", i, msg.Role, strings.Join(blockParts, ",")))
	}
	return strings.Join(parts, " | ")
}

func (w *workerRunner) serveOpenInference(ctx context.Context, req *proto.OpenInferenceRequest, safeSend func(*proto.HostToWorker) error) {
	id := req.GetId()
	emit := func(ev *proto.OpenInferenceEvent) {
		ev.Id = id
		if sendErr := safeSend(&proto.HostToWorker{Msg: &proto.HostToWorker_OpenEvent{OpenEvent: ev}}); sendErr != nil {
			log.Printf("[workerRunner] send open event id=%d: %v", id, sendErr)
		}
	}
	fail := func(err error) {
		emit(&proto.OpenInferenceEvent{Kind: &proto.OpenInferenceEvent_Error{Error: err.Error()}})
	}

	if w.openProvider == nil {
		fail(fmt.Errorf("open inference unavailable: no open provider on host"))
		return
	}
	prov := w.openProvider()
	if prov == nil {
		fail(fmt.Errorf("open inference unavailable: open provider not configured"))
		return
	}
	chatReq, err := UnmarshalChatRequest(req.GetRequest())
	if err != nil {
		fail(fmt.Errorf("open inference: unmarshal request: %w", err))
		return
	}
	log.Printf("[workerRunner] open inference request: id=%d provider=%s model=%s temp=%s max_tokens=%d tools=%v lean_subagent_prompt=%t system_prefix=%q messages=%d message_shapes=%s",
		id, prov.Name(), chatReq.Model, workerTemperatureForLog(chatReq.Temperature), chatReq.MaxTokens, workerToolNamesForLog(chatReq.Tools), strings.Contains(chatReq.System, "bounded Cercano sub-agent"), workerTruncateRunes(strings.TrimSpace(chatReq.System), 120), len(chatReq.Messages), workerMessageShapesForLog(chatReq.Messages))
	rdr, err := prov.StreamChat(ctx, chatReq)
	if err != nil {
		fail(err)
		return
	}
	defer rdr.Close()
	for {
		ev, ok, err := rdr.Next()
		if err != nil {
			fail(err)
			return
		}
		if !ok {
			emit(&proto.OpenInferenceEvent{Kind: &proto.OpenInferenceEvent_Done{Done: true}})
			return
		}
		emit(&proto.OpenInferenceEvent{Kind: &proto.OpenInferenceEvent_Event{Event: MarshalStreamEvent(ev)}})
	}
}
