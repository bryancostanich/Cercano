package worker

import (
	"cercano/source/server/internal/reasoningexperiment"
	"cercano/source/server/internal/visioninspect"
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/cloudfactory"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/failurelog"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	providerssvc "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/profilechain"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	ollamallm "cercano/source/server/internal/llm/ollama"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/routinglog"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/toolstack"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/watchdog"
	pkgcfg "cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WorkerServer implements the gRPC Worker service (worker-side).
type WorkerServer struct {
	accountingCollector *telemetry.AccountingCollector
	accountingClosing   bool
	accountingTurns     map[string]context.CancelFunc
	accountingWorkers   sync.WaitGroup
	accountingCloseOnce sync.Once
	accountingCloseDone chan struct{}
	accountingCloseErr  error

	accountingMu        sync.Mutex
	accountingTransport *workerAccountingWriter
	proto.UnimplementedWorkerServer

	// providerFactory overrides provider construction for tests.
	// When nil, the production path builds from ConfigSnapshot.
	providerFactory func(*proto.StartTurn) (providerssvc.Resolver, error)

	// toolsFactory overrides tool registry construction for tests.
	// When nil, the production path installs the full capability registry.
	toolsFactory func(*proto.StartTurn) (runner.ToolSvc, error)
}

// New creates a WorkerServer for production use.
func New() *WorkerServer { return &WorkerServer{} }

// NewWithFactories creates a WorkerServer with injected factories for testing.
func NewWithFactories(
	pf func(*proto.StartTurn) (providerssvc.Resolver, error),
	tf func(*proto.StartTurn) (runner.ToolSvc, error),
) *WorkerServer {
	return &WorkerServer{providerFactory: pf, toolsFactory: tf}
}

// RunTurn is the bidi RPC handler.
func (w *WorkerServer) RunTurn(stream proto.Worker_RunTurnServer) error {
	return w.runTurn(stream, false)
}
func (w *WorkerServer) runTurn(stream proto.Worker_RunTurnServer, authRecovery bool) error {
	// First message must be StartTurn.
	firstMsg, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.Internal, "worker: recv first message: %v", err)
	}
	start := firstMsg.GetStart()
	if start == nil {
		return status.Errorf(codes.InvalidArgument, "worker: first message must be StartTurn")
	}

	// Build execution context: cancel when host sends Cancel.
	parent := stream.Context()
	if start.Accounting != nil {
		scoped, release, scopeErr := w.beginAccountingTurn(parent, start.Accounting, start.GetConversationId())
		if scopeErr != nil {
			return scopeErr
		}
		defer release()
		parent = scoped
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Serialized sender: all outbound messages go through one goroutine.
	sndr := newSender(stream)

	// Permission requester: round-trips PermissionRequest↔PermissionResponse.
	permReq := newStreamPermissionRequester(sndr)

	// Credential source: round-trips CredentialRequest↔CredentialResponse.
	// Created before the recv loop so it is ready to receive routed responses.
	credSource := newStreamCredentialSource(sndr)

	// Open provider proxy: the worker has no local runtime manager, so it
	// forwards host-managed open-model inference to the host over this stream.
	// Routed OpenInferenceEvents deliver here. Created before the recv loop for
	// the same reason as credSource. Its Name follows the StartTurn config
	// snapshot so dispatch logs/headers report the active runtime correctly.
	openProxy := newHostManagedOpenProxy(sndr, start.GetConfig().GetOpenRuntime())

	// Sub-agent persistence proxy: a worker-side dispatch creates its sub-agent
	// conversation row and persists its turns on the host via this stream proxy
	// (the worker has no local store). Built before buildDeps so the tool stack
	// can wire it, same as credSource.
	subPersist := &streamSubagentPersist{sndr: sndr, gen: start.GetGen()}

	// Session profile proxy: session-control capabilities such as suggest_plan
	// must mutate the host's live profile broker, not a worker-local copy.
	profileCtl := newStreamSessionProfileController(sndr, start.GetConversationId())
	authRequest := newStreamAuthentication(sndr)
	runtimeControl := newStreamRuntimeControl(sndr)

	// MCP proxy: host-side MCP tools advertised in StartTurn become worker-side
	// proxy tools that call back over this stream. The worker owns no MCP
	// connections — the host does.
	mcpControl := newStreamMCPControl(sndr)

	// The permission store is built inside buildDeps (below), but the recv loop
	// starts first and may receive a PermissionUpdate at any time. Publish the
	// store through an atomic pointer so an update that races construction is
	// simply dropped (StartTurn's values still apply) rather than panicking.
	var permStoreRef atomic.Pointer[agent.PermissionStore]

	// Recv loop: routes incoming HostToWorker messages from the host.
	recvDone := make(chan struct{})
	go func() {
		defer close(recvDone)
		for {
			msg, err := stream.Recv()
			if err != nil {
				return // stream closed or host hung up
			}
			switch {
			case msg.GetRuntimeResponse() != nil:
				runtimeControl.deliver(msg.GetRuntimeResponse())
			case msg.GetMcpResponse() != nil:
				mcpControl.deliver(msg.GetMcpResponse())
			case msg.GetPermUpdate() != nil:
				// Mid-turn permission change on the host. Apply it so the gate
				// sees the same values an in-process turn would re-read from
				// permissions.yaml on its NEXT decision — a tightening must not
				// wait for the turn to end.
				u := msg.GetPermUpdate()
				if store := permStoreRef.Load(); store != nil {
					m := agent.ModePermissive
					if parsed, err := agent.ParseMode(u.GetMode()); err == nil {
						m = parsed
					}
					store.ApplyRuntimeUpdate(m, u.GetMcpAllow())
				}
			case msg.GetAuthResponse() != nil:
				authRequest.deliver(msg.GetAuthResponse())
			case msg.GetPermResponse() != nil:
				permReq.deliver(msg.GetPermResponse())
			case msg.GetCredResponse() != nil:
				credSource.deliver(msg.GetCredResponse())
			case msg.GetOpenEvent() != nil:
				openProxy.deliver(msg.GetOpenEvent())
			case msg.GetProfileResponse() != nil:
				profileCtl.deliver(msg.GetProfileResponse())
			case msg.GetCancel() != nil:
				cancel()
				return
			}
		}
	}()

	// Build Deps from StartTurn.
	deps, buildErr := w.buildDeps(ctx, start, credSource, openProxy, subPersist, profileCtl, mcpControl, &permStoreRef, runtimeControl.Restart)
	if buildErr != nil {
		sndr.close()
		cancel() // returning finalizes the stream; the recv goroutine unwinds on the Recv error
		return status.Errorf(codes.Internal, "worker: build deps: %v", buildErr)
	}

	if closer, ok := deps.VisionStore.(interface{ Close() error }); ok {
		defer func() {
			if err := closer.Close(); err != nil {
				log.Printf("[vision] worker temporary attachment cleanup: %v", err)
			}
		}()
	}

	// Decode history.
	history := make([]llm.Message, 0, len(start.GetHistory()))
	for _, pm := range start.GetHistory() {
		m, err := UnmarshalMessage(pm)
		if err != nil {
			sndr.close()
			cancel()
			return status.Errorf(codes.InvalidArgument, "worker: unmarshal history: %v", err)
		}
		history = append(history, m)
	}

	// Wire PersistFunc and TurnHistory.
	pf := &streamPersistFunc{sndr: sndr, gen: start.GetGen()}
	deps.Persist = &preloadedHistory{
		history:        history,
		projectContext: start.GetProjectContext(),
		pf:             pf,
	}

	// Wire EventSink.
	sink := &streamEventSink{sndr: sndr}

	// Wire PermissionRequester as a runner.PermissionRequester func.
	permFn := runner.PermissionRequester(permReq.Request)

	// Wire PersistFunc.
	persistFn := runner.PersistFunc(pf.persist)

	// Build Request.
	req := runner.Request{
		ConversationID: start.GetConversationId(),
		Input:          start.GetInput(),
		WorkDir:        start.GetWorkDir(),
		DebugMode:      start.GetDebugMode(),
		Gen:            start.GetGen(),
	}
	if authRecovery {
		req.AuthRecovery = authRequest.Request
	}
	for _, img := range start.GetImages() {
		req.Images = append(req.Images, agent.InlineImage{
			Index:     int(img.GetIndex()),
			MediaType: img.GetMediaType(),
			Data:      img.GetData(),
		})
	}

	// Run the turn (with panic recovery).
	var result runner.Result
	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Log the FULL stack to the worker's stderr (teed to the host log
				// via spawn.go) before recover() swallows it — a recovered panic
				// with only its value is undebuggable across the process boundary.
				stack := debug.Stack()
				log.Printf("[worker] PANIC in RunTurn: %v\n%s", r, stack)
				runErr = fmt.Errorf("worker: panic in RunTurn: %v", r)
			}
		}()
		result, runErr = runner.New(deps).RunTurn(ctx, req, sink, permFn, persistFn)
	}()

	// Flush sender before the terminal send: close blocks until the sender
	// goroutine exits, so the direct Send below can't race it.
	sndr.close()

	// Send the terminal message NOW — do NOT wait for the host to half-close
	// the stream. The host keeps its send direction open until it reads TurnDone
	// (it can then Cancel/close), so blocking the terminal send on recvDone would
	// deadlock. Returning from the handler finalizes the stream, which unwinds
	// the recv goroutine (its Recv errors); cancel() releases any ctx waiters.
	defer cancel()

	// Send final outcome directly on stream (sender goroutine is gone).
	if runErr != nil {
		_ = stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_Error{Error: &proto.TurnError{
			Message: runErr.Error(),
		}}})
		return nil
	}
	_ = stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_Done{Done: &proto.TurnDone{
		FinalText:    result.FinalText,
		Route:        marshalServingRoute(result.Route),
		Model:        result.Model,
		IsCloud:      result.IsCloud,
		InputTokens:  int64(result.InputTokens),
		OutputTokens: int64(result.OutputTokens),
		Notice:       result.Notice,
	}}})
	return nil
}

// ─── buildDeps ────────────────────────────────────────────────────────────────

func (w *WorkerServer) buildDeps(ctx context.Context, start *proto.StartTurn, credSource *streamCredentialSource, openProxy *streamOpenProvider, subPersist *streamSubagentPersist, profileCtl *streamSessionProfileController, mcpControl *streamMCPControl, permStoreRef *atomic.Pointer[agent.PermissionStore], restart ...runtimeRestartFunc) (runner.Deps, error) {
	// Build config from snapshot.
	cfg := ConfigFromSnapshot(start.GetConfig())
	cfgService := cfgsvc.New("", cfg, secrets.NewMemory())

	// Host-resolved capability evidence for this turn. The worker never
	// discovers models; an identity absent from this snapshot is UNKNOWN here,
	// which blocks images and leaves the conventional context fallback in place.
	evidence := UnmarshalModelMetadata(start.GetConfig().GetModelMetadata())

	cloudEvidence := func(model string) modelmetadata.Evidence {
		if model == "" {
			return modelmetadata.Evidence{}
		}
		// Active profile first, then backup: a failover attempt addresses a
		// model on the backup's endpoint, and that leg must budget against its
		// own window rather than inheriting the primary's.
		for _, name := range []string{cfg.ActiveCloudProfile, cfg.BackupCloudProfile} {
			if name == "" {
				continue
			}
			prof, ok := profileByName(cfg.CloudProfiles, name)
			if !ok {
				continue
			}
			if ev, found := evidence.Lookup(modelmetadata.Identity{Provider: prof.Provider, BaseURL: prof.BaseURL, Route: prof.Route, Model: model}); found {
				return ev
			}
		}
		return modelmetadata.Evidence{}
	}
	// Confirmed image capability for the model a cloud client is built around.
	// Unknown is not permission: an unconfirmed model gets a text-only client,
	// so tool results carrying images degrade to text instead of being sent to a
	// model that may not understand them.
	visionConfirmed := func(model string) bool {
		return cloudEvidence(model).Vision == modelmetadata.VisionSupported
	}

	// Build Providers.
	var provSvc providerssvc.Resolver
	if w.providerFactory != nil {
		var err error
		provSvc, err = w.providerFactory(start)
		if err != nil {
			return runner.Deps{}, fmt.Errorf("build providers: %w", err)
		}
	} else {
		var err error
		provSvc, err = buildWorkerProviders(ctx, cfg, credSource, openProxy, visionConfirmed, func(p pkgcfg.CloudProfile, model string) modelmetadata.Evidence {
			ev, _ := evidence.Lookup(modelmetadata.Identity{Provider: p.Provider, BaseURL: p.BaseURL, Route: p.Route, Model: model})
			return ev
		})
		if err != nil {
			return runner.Deps{}, fmt.Errorf("build providers: %w", err)
		}
	}

	// Build Perms FIRST — the tool stack's sub-agent dispatch needs the broker.
	// The store's MODE must match the host's: whether a tool tier prompts at all
	// depends on it, and the actual prompt round-trips to the host via the stream
	// requester. Defaulting to permissive when the host is strict would silently
	// auto-run writes the user asked to be prompted for. Empty/unparseable falls
	// back to permissive (the host default).
	mode := agent.ModePermissive
	if m, err := agent.ParseMode(start.GetPermissionMode()); err == nil {
		mode = m
	}
	// The allowlist travels with the mode for the same reason: the worker cannot
	// read the host's permissions.yaml, and a store built without it reports
	// NOTHING as allowlisted — so every allowlisted MCP tool would re-prompt on
	// worker turns while behaving correctly in-process.
	permStore := agent.NewStaticPermissionStoreWithMCPAllow(mode, start.GetMcpAllow())
	permBroker := permissions.New(permStore, nil, nil)
	// Publish for the recv loop so a mid-turn PermissionUpdate can tighten this
	// store's mode/allowlist, matching the in-process per-decision re-read.
	if permStoreRef != nil {
		permStoreRef.Store(permStore)
	}

	// Build the worker's dispatch engine ONCE via the shared internal/toolstack
	// builder — the SAME assembly the host uses — with a real project-context
	// loader and tier→model resolution mirroring the host's DispatchModelFor. This
	// is what lets a capability that dispatches (local, the co-processor caps,
	// review, the web caps, and the dispatch sub-agent) find a live engine in
	// worker turns exactly as in-process. The same engine backs the watchdog's
	// OneShot lane (which passes an explicit model override, so tier resolution
	// never bites it) and the capability tool stack below.
	ctxLoader := projectctx.NewLoader()
	engine := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() inference.Tiers {
			return provSvc.Candidates()
		},
		LocusMode:      func() locus.Mode { m, _ := locus.ParseMode(cfg.LocusMode); return m },
		CtxLoader:      ctxLoader,
		ModelFor:       workerDispatchModelFor(cfg),
		TaskAssignment: cfg.TaskAssignment,
		DestinationModelFor: func(sel inference.Selection, tier pkgcfg.Tier) string {
			if !sel.IsCloud {
				return openTierModel(cfg, tier)
			}
			if p, ok := cfg.Profile(sel.Profile); ok {
				return cfg.ModelProfiles.ResolveCloudModelForTier(p, tier)
			}
			return ""
		},
	})

	// Build the shared vision-as-tool store + service, mirroring the host. Local
	// Vision is cloud-first whenever the locus permits cloud, with local/open as
	// fallback and open_only as a hard no-cloud boundary. The store is
	// per-process, so images added during this worker turn live only for the turn
	// — which is exactly the scope inspect_image needs, as it runs within the same
	// turn's tool loop. The SAME store instance is handed to buildWorkerToolSvc
	// (lookup) and runner.Deps below (rewrite).
	visionStore, visionSvc := toolstack.BuildVision(toolstack.VisionDeps{
		OpenProvider: func() inference.Provider { return provSvc.Open() },
		OpenVisionModel: func() (string, bool) {
			id := openTierModel(cfg, pkgcfg.TierVision)
			return id, id != ""
		},
		CloudProvider: func() inference.Provider { return provSvc.Cloud() },
		CloudTarget:   func() (visioninspect.Resolved, bool) { return toolstack.ResolveCloudVision(provSvc.Cloud()) },
		Mode:          func() locus.Mode { m, _ := locus.ParseMode(cfg.LocusMode); return m },
	})

	failureLog, err := failurelog.NewWriter("")
	if err != nil {
		log.Printf("[failures] worker open log: %v", err)
	}

	// Build Tools. The test hook (w.toolsFactory) still wins when set; otherwise
	// assemble the full capability/tool stack wired to the worker's engine.
	var toolSvc runner.ToolSvc
	if w.toolsFactory != nil {
		var err error
		toolSvc, err = w.toolsFactory(start)
		if err != nil {
			return runner.Deps{}, fmt.Errorf("build tools: %w", err)
		}
	} else {
		diagnostic, _ := provSvc.(reasoningexperiment.Service)
		toolSvc = buildWorkerToolSvcWithDiagnostic(permBroker, engine, ctxLoader, provSvc.Cloud(), provSvc.Open(), cfg, subPersist, profileCtl.SetProfile, visionSvc, failureLog, diagnostic, restart...)
	}

	// Register a proxy per host-advertised MCP tool. Done AFTER the built-in
	// stack so built-ins win a name collision, matching the host ordering where
	// the built-in registry is populated before MCP servers connect. Proxies
	// report OriginMCP, so the tool loop's gate treats them as third-party
	// exactly as the host does.
	if mcpControl != nil && len(start.GetMcpTools()) > 0 && toolSvc != nil {
		if n := registerMCPProxies(toolSvc.Registry(), start.GetMcpTools(), mcpControl); n > 0 {
			log.Printf("[worker] registered %d host MCP tool proxies", n)
		}
	}

	// Build the protocol-supervision watchdog from the snapshotted config
	// (default-OFF; nil when disabled — identical to in-process). Its fast-model
	// OneShot lane routes through the worker's engine (built above), so the model
	// call runs locally in the worker and never round-trips to the host. wd is nil
	// when the watchdog is disabled — the runner's live accessor (c.d.Watchdog())
	// then yields nil, the correct default-off behavior.
	wd := buildWorkerWatchdog(cfg, engine)
	routeLog, err := routinglog.NewWriter("")
	if err != nil {
		log.Printf("[routing] worker open log: %v", err)
	}
	if provSvc != nil {
		provSvc.SetRoutingLog(routeLog)
	}

	return runner.Deps{
		Providers: provSvc,
		Tools:     toolSvc,
		Persist:   nil, // set by caller after history decode
		Config:    cfgService,
		Perms:     permBroker,
		Agent:     nil,
		Watchdog:  func() *watchdog.Watchdog { return wd },
		// Shared with the worker's inspect_image VisionService (built above): the
		// runner registers image placeholders here; the tool looks them up.
		VisionStore: visionStore,
		// Per-attempt destination capacity, mirroring the host so an isolated
		// turn budgets identically.
		CloudContextWindow: func(model string) (int, bool) {
			w := cloudEvidence(model).ContextWindow
			return w, w > 0
		},
		RoutingLog: routeLog,
		FailureLog: failureLog,
	}, nil
}

// ─── workerResolver ───────────────────────────────────────────────────────────

// workerResolver is a minimal providers.Resolver for the worker.
// It holds pre-built cloud + open providers and delegates model selection to
// the config service.
type workerResolver struct {
	diagnosticBuild func(pkgcfg.CloudProfile) (inference.Provider, error)
	secondaryProv   inference.Provider
	cloudProv       inference.Provider
	openProv        inference.Provider
	cfgSvc          cfgsvc.Service
}

// profileByName selects a cloud profile by name, mirroring
// providers.profileByName. An empty name (no active profile) matches nothing,
// so ok is false and no cloud provider is built.
func profileByName(profiles []pkgcfg.CloudProfile, name string) (pkgcfg.CloudProfile, bool) {
	for _, pr := range profiles {
		if pr.Name == name {
			return pr, true
		}
	}
	return pkgcfg.CloudProfile{}, false
}

func buildWorkerProviders(ctx context.Context, cfg pkgcfg.Config, credSource credentialFetcher, openProxy *streamOpenProvider, modelSupportsVision func(string) bool, scoped ...func(pkgcfg.CloudProfile, string) modelmetadata.Evidence) (providerssvc.Resolver, error) {
	cfgService := cfgsvc.New("", cfg, secrets.NewMemory())
	r := &workerResolver{cfgSvc: cfgService}

	build := func(prof pkgcfg.CloudProfile) (inference.Provider, error) {
		confirmed := modelSupportsVision
		if len(scoped) > 0 {
			confirmed = func(model string) bool { return scoped[0](prof, model).Vision == modelmetadata.VisionSupported }
		}
		opts := cloudfactory.Options{ModelSupportsVision: confirmed}
		metadataFor := func(model string) modelmetadata.Evidence {
			if len(scoped) == 0 {
				return modelmetadata.Evidence{}
			}
			return scoped[0](prof, model)
		}
		create := func() (inference.Provider, error) {
			provider, err := cloudfactory.BuildCloudProvider(prof, "", opts)
			if err != nil {
				return nil, err
			}
			return profilechain.GuardVision(provider, confirmed, metadataFor), nil
		}
		if prof.Flavor == cloudfactory.FlavorResponses && prof.Route == cloudfactory.RouteChatGPT {
			opts.TokenSource = &streamTokenSource{creds: credSource, profileName: prof.Name}
			return create()
		}
		if prof.Flavor == cloudfactory.FlavorMessages && prof.Route == cloudfactory.RouteSubscription {
			opts.AnthropicTokenSource = &anthropicStreamTokenSource{creds: credSource, profileName: prof.Name}
			return create()
		}
		key := ""
		var keyErr error
		if credSource != nil && prof.Flavor != cloudfactory.FlavorBedrock {
			key, _, keyErr = credSource.Fetch(ctx, prof.Name)
		}
		if keyErr != nil {
			var typed *llm.CredentialError
			if errors.As(keyErr, &typed) {
				return nil, keyErr
			}
		}
		if err := cloudfactory.ValidateStaticCredential(prof, key, keyErr); err != nil {
			return nil, err
		}
		provider, err := cloudfactory.BuildCloudProvider(prof, key, opts)
		if err != nil {
			return nil, err
		}
		return profilechain.GuardVision(provider, confirmed, metadataFor), nil
	}
	r.diagnosticBuild = build
	r.cloudProv, _ = profilechain.Build(cfg, pkgcfg.DestinationPrimary, build, workerChainEvents(pkgcfg.DestinationPrimary))
	r.secondaryProv, _ = profilechain.Build(cfg, pkgcfg.DestinationSecondary, build, workerChainEvents(pkgcfg.DestinationSecondary))

	// Build the open provider, mirroring the host's openProviderFor: when the
	// open runtime is llama-server the worker has no local access to it (the
	// runtime manager is a host singleton), so route open inference through the
	// host proxy over the RunTurn stream. Otherwise fall back to a direct Ollama
	// client when a URL is configured. This is the fix for open dispatches
	// hitting a dead Ollama endpoint under a host-managed open runtime
	// (llama-server or mistral.rs) — both live only on the host, so the worker
	// proxies to the host, which serves them via its own openProviderFor. The
	// proxy carries no runtime label; the host picks the engine from its config.
	if runtimeIsHostManaged(cfg.OpenRuntime) {
		r.openProv = openProxy
	} else if cfg.OllamaURL != "" {
		r.openProv = ollamallm.NewClient(ollamallm.Config{
			BaseURL: cfg.OllamaURL,
			// The everyday open model — the host resolved it (override ⊕ catalog)
			// and sent it as the active runtime's override, so OverrideFor
			// returns exactly what the host intended.
			Model: openTierModel(cfg, pkgcfg.TierEveryday),
		})
	}

	return r, nil
}

// runtimeIsHostManaged reports whether an open runtime lives ONLY on the host
// (its runtime manager is a host singleton) and therefore cannot be reached
// directly from a worker — the worker must proxy open inference to the host
// over the RunTurn stream. This is a capability question ("does this runtime
// require host-proxy?"), not really a name list; it is expressed by name here
// because the worker has no access to the host's RuntimeCapabilities. When a
// new host-managed runtime is added, list it here (the ONE place the worker's
// open-routing decision is made) — an Ollama-style, URL-reachable runtime is
// left off so it routes to a direct client instead.
func runtimeIsHostManaged(runtime string) bool {
	switch runtime {
	case "llama_server", "mistralrs":
		return true
	default:
		return false
	}
}

func (r *workerResolver) Main() (inference.Provider, bool, bool, error) {
	candidates := r.Candidates()
	mode := candidates.Mode
	assignment := candidates.TaskFor(pkgcfg.TaskChat)
	model := candidates.ModelFor(inference.Selection{IsCloud: false}, assignment.Quality.CapabilityTier())
	if candidates.OpenReady != nil && !candidates.OpenReady(model) {
		candidates.Open = nil
	}
	sel, err := inference.SelectDestination(mode, assignment.Destination, candidates)
	if err != nil {
		return nil, false, false, err
	}
	if sel.IsCloud {
		model = inference.TargetForCall(sel.Provider, inference.Call{Tier: string(assignment.Quality.CapabilityTier())}).Model
	}
	return inference.WithTaskRoute(sel.Provider, pkgcfg.TaskChat, assignment, sel.PolicyDestination, model), sel.IsCloud, sel.FellBack, nil
}

func (r *workerResolver) Candidates() inference.Tiers {
	c := r.cfgSvc.Get()
	mode, _ := locus.ParseMode(c.LocusMode)
	modelFor := func(sel inference.Selection, t pkgcfg.Tier) string {
		if !sel.IsCloud {
			return openTierModel(c, t)
		}
		if profile, ok := c.Profile(sel.Profile); ok {
			return c.ModelProfiles.ResolveCloudModelForTier(profile, t)
		}
		return ""
	}
	return inference.Tiers{Mode: mode, ModelFor: modelFor, OpenReady: func(model string) bool { return dispatch.OpenModelReadyFor(c, model) }, Cloud: r.cloudProv, Open: r.openProv, TaskFor: c.TaskAssignment, ResolveDestination: c.ResolveDestination, Destinations: map[pkgcfg.Destination]inference.Candidate{
		pkgcfg.DestinationPrimary:   {Provider: r.cloudProv, Profile: c.ActiveCloudProfile, IsCloud: true},
		pkgcfg.DestinationSecondary: {Provider: r.secondaryProv, Profile: c.SecondaryCloudProfile, IsCloud: true},
	}}
}
func (r *workerResolver) MainModel(isCloud bool) string {
	c := r.cfgSvc.Get()
	a := c.TaskAssignment(pkgcfg.TaskChat)
	if isCloud {
		destination, err := c.ResolveDestination(a.Destination)
		if err != nil {
			return ""
		}
		name, _ := c.DestinationProfiles(destination)
		if p, ok := c.Profile(name); ok {
			return c.ModelProfiles.ResolveCloudModelForTier(p, a.Quality.CapabilityTier())
		}
		return ""
	}
	return openTierModel(c, a.Quality.CapabilityTier())
}
func (r *workerResolver) PrimaryModel() string {
	c := r.cfgSvc.Get()
	a := c.TaskAssignment(pkgcfg.TaskChat)
	destination, err := c.ResolveDestination(a.Destination)
	if err != nil {
		return ""
	}
	cloud := destination != pkgcfg.DestinationLocal && c.LocusMode != "open_only" && (destination == pkgcfg.DestinationSecondary || c.LocusMode != "open_primary")
	return r.MainModel(cloud)
}
func (r *workerResolver) Rebuild() error              { return nil }
func (r *workerResolver) InstallAbsentCloud(_ string) { r.cloudProv = nil }
func (r *workerResolver) Cloud() inference.Provider   { return r.cloudProv }
func (r *workerResolver) Open() inference.Provider    { return r.openProv }
func (r *workerResolver) ActiveCloudModel() string {
	return r.MainModel(true)
}
func (r *workerResolver) LocusMode() string                                               { return r.cfgSvc.Get().LocusMode }
func (r *workerResolver) Router() providerssvc.RouterCloudUpdater                         { return nil }
func (r *workerResolver) Registry() *engine.EngineRegistry                                { return nil }
func (r *workerResolver) CatalogManager() *ollamacatalog.Manager                          { return nil }
func (r *workerResolver) SetCloudLLMProvider(p inference.Provider)                        { r.cloudProv = p }
func (r *workerResolver) SetOpenLLMProvider(p inference.Provider)                         { r.openProv = p }
func (r *workerResolver) SetOpenProviderFactory(_ func(pkgcfg.Config) inference.Provider) {}
func (r *workerResolver) CloudLLMProvider() inference.Provider                            { return r.cloudProv }
func (r *workerResolver) OpenLLMProvider() inference.Provider                             { return r.openProv }
func (r *workerResolver) Reconfigure(_ providerssvc.ReconfigureArgs)                      {}
func (r *workerResolver) SetCatalogManager(_ *ollamacatalog.Manager)                      {}
func (r *workerResolver) SetUsageSink(_ func(usage.Usage))                                {}
func (r *workerResolver) SetRoutingLog(_ *routinglog.Writer)                              {}

// SetModelSupportsVision is a no-op here: the worker's providers are built once
// per turn in buildWorkerProviders, which passes the capability oracle to
// cloudfactory directly rather than installing it after the fact.
func (r *workerResolver) SetModelSupportsVision(_ func(model string) bool) {}

// The worker's capability/tool stack is assembled by buildWorkerToolSvc (see
// worker_dispatch.go) through the shared internal/toolstack builder — the same
// assembly the host uses — so worker turns wire an identical Services.

func (r *workerResolver) SetProfileModelEvidence(func(pkgcfg.CloudProfile, string) modelmetadata.Evidence) {
}

func workerChainEvents(d pkgcfg.Destination) func(resilience.Event) {
	return func(ev resilience.Event) {
		log.Printf("[worker] %s resilience %s (%s, %s): %s: %v", d, ev.Action, ev.Stage, ev.Class, ev.Notice(), ev.Err)
	}
}

func (r *workerResolver) RunReasoningDiagnostic(ctx context.Context, spec reasoningexperiment.Spec) (reasoningexperiment.Report, error) {
	if r.diagnosticBuild == nil {
		return reasoningexperiment.Report{}, fmt.Errorf("reasoning diagnostic unavailable")
	}
	return reasoningexperiment.Run(ctx, r.cfgSvc.Get(), spec, r.diagnosticBuild)
}
