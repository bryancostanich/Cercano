package worker

import (
	"context"
	"fmt"
	"log"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	providerssvc "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/fallback"
	ollamallm "cercano/source/server/internal/llm/ollama"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/watchdog"
	pkgcfg "cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WorkerServer implements the gRPC Worker service (worker-side).
type WorkerServer struct {
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
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	// Serialized sender: all outbound messages go through one goroutine.
	sndr := newSender(stream)

	// Permission requester: round-trips PermissionRequest↔PermissionResponse.
	permReq := newStreamPermissionRequester(sndr)

	// Credential source: round-trips CredentialRequest↔CredentialResponse.
	// Created before the recv loop so it is ready to receive routed responses.
	credSource := newStreamCredentialSource(sndr)

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
			case msg.GetPermResponse() != nil:
				permReq.deliver(msg.GetPermResponse())
			case msg.GetCredResponse() != nil:
				credSource.deliver(msg.GetCredResponse())
			case msg.GetCancel() != nil:
				cancel()
				return
			}
		}
	}()

	// Build Deps from StartTurn.
	deps, buildErr := w.buildDeps(ctx, start, credSource)
	if buildErr != nil {
		sndr.close()
		cancel() // returning finalizes the stream; the recv goroutine unwinds on the Recv error
		return status.Errorf(codes.Internal, "worker: build deps: %v", buildErr)
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
		Gen:            start.GetGen(),
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
		Model:        result.Model,
		IsCloud:      result.IsCloud,
		InputTokens:  int64(result.InputTokens),
		OutputTokens: int64(result.OutputTokens),
		Notice:       result.Notice,
	}}})
	return nil
}

// ─── buildDeps ────────────────────────────────────────────────────────────────

func (w *WorkerServer) buildDeps(ctx context.Context, start *proto.StartTurn, credSource *streamCredentialSource) (runner.Deps, error) {
	// Build config from snapshot.
	cfg := ConfigFromSnapshot(start.GetConfig())
	cfgService := cfgsvc.New("", cfg, secrets.NewMemory())

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
		provSvc, err = buildWorkerProviders(ctx, cfg, credSource)
		if err != nil {
			return runner.Deps{}, fmt.Errorf("build providers: %w", err)
		}
	}

	// Build Tools.
	var toolSvc runner.ToolSvc
	if w.toolsFactory != nil {
		var err error
		toolSvc, err = w.toolsFactory(start)
		if err != nil {
			return runner.Deps{}, fmt.Errorf("build tools: %w", err)
		}
	} else {
		toolSvc = buildWorkerTools()
	}

	// Build Perms: the store's MODE must match the host's — whether a tool tier
	// prompts at all depends on it, and the actual prompt round-trips to the host
	// via the stream requester. Defaulting to permissive when the host is strict
	// would silently auto-run writes the user asked to be prompted for.
	// Empty/unparseable falls back to permissive (the host default).
	mode := agent.ModePermissive
	if m, err := agent.ParseMode(start.GetPermissionMode()); err == nil {
		mode = m
	}
	permStore := agent.NewStaticPermissionStore(mode)
	permBroker := permissions.New(permStore, nil, nil)

	// Build the protocol-supervision watchdog from the snapshotted config
	// (default-OFF; nil when disabled — identical to in-process). Its fast-model
	// OneShot lane routes through a dispatch engine wired to the WORKER's own
	// provider resolver, so the model call runs locally in the worker and never
	// round-trips to the host. wd is nil when the watchdog is disabled — the
	// runner's live accessor (c.d.Watchdog()) then yields nil, the correct
	// default-off behavior.
	wd := buildWorkerWatchdog(cfg, buildWorkerEngine(provSvc, cfg))

	return runner.Deps{
		Providers: provSvc,
		Tools:     toolSvc,
		Persist:   nil, // set by caller after history decode
		Config:    cfgService,
		Perms:     permBroker,
		Agent:     nil,
		Watchdog:  func() *watchdog.Watchdog { return wd },
	}, nil
}

// ─── workerResolver ───────────────────────────────────────────────────────────

// workerResolver is a minimal providers.Resolver for the worker.
// It holds pre-built cloud + open providers and delegates model selection to
// the config service.
type workerResolver struct {
	cloudProv llm.Provider
	openProv  llm.Provider
	cfgSvc    cfgsvc.Service
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

func buildWorkerProviders(ctx context.Context, cfg pkgcfg.Config, credSource credentialFetcher) (providerssvc.Resolver, error) {
	cfgService := cfgsvc.New("", cfg, secrets.NewMemory())
	r := &workerResolver{cfgSvc: cfgService}

	// Build cloud provider from the ACTIVE profile, selected BY NAME (mirroring
	// the host's rebuildCloud / ActiveProfile — the active profile is not
	// necessarily CloudProfiles[0]). Selecting [0] here builds the wrong
	// provider (route/flavor/credential-profile) whenever the active profile
	// isn't first, while the worker's own model resolution correctly uses the
	// named active profile — a silent worker/in-process divergence.
	if prof, ok := profileByName(cfg.CloudProfiles, cfg.ActiveCloudProfile); ok {
		var prov llm.Provider
		var buildErr error

		if prof.Flavor == cloudfactory.FlavorResponses && prof.Route == cloudfactory.RouteChatGPT {
			// ChatGPT subscription: use a stream-backed token source so the host
			// owns refresh and OAuth — the worker never holds the credential.
			ts := &streamTokenSource{creds: credSource, profileName: prof.Name}
			prov, buildErr = cloudfactory.BuildCloudProvider(prof, "", cloudfactory.Options{TokenSource: ts})
		} else {
			// Static-key route: fetch the API key once via the stream.
			key, _, err := credSource.Fetch(ctx, prof.Name)
			if err != nil {
				// Cloud stays unbuilt; the turn degrades to the open provider
				// (locus graceful-degradation). Do NOT touch r.openProv here —
				// it is built separately below and clearing it would be a bug.
				log.Printf("[worker] credential fetch failed for profile %q: %v; continuing without cloud", prof.Name, err)
			} else {
				prov, buildErr = cloudfactory.BuildCloudProvider(prof, key)
			}
		}
		if buildErr != nil {
			log.Printf("[worker] cloud provider build failed: %v; continuing without cloud", buildErr)
		} else {
			// A configured backup profile wraps the primary in a fallback composite,
			// mirroring the in-process providers.wrapBackup path. Everything
			// downstream sees one provider; a missing/unbuildable backup leaves the
			// primary bare. The backup's credential is fetched via the SAME stream
			// credential proxy, keyed by the backup profile name.
			r.cloudProv = wrapWorkerBackup(ctx, prov, prof.Name, cfg, credSource)
		}
	}
	// No active profile → cloudProv remains nil.

	// Build open (Ollama) provider if URL is set.
	if cfg.OllamaURL != "" {
		r.openProv = ollamallm.NewClient(ollamallm.Config{
			BaseURL: cfg.OllamaURL,
			// Read the open model from the everyday-open tier (OpenChatModel),
			// NOT the legacy cfg.OpenModel field: config normalization
			// (finalizeModelTiers) migrates open_model into Tiers.Everyday.Open
			// and BLANKS cfg.OpenModel, so on the host's normalized config — the
			// one snapshotted here — cfg.OpenModel is always "". Mirrors the host.
			Model: (&cfg).OpenChatModel(),
		})
	}

	return r, nil
}

// wrapWorkerBackup mirrors providers.wrapBackup but sources the backup
// credential via the stream credential proxy instead of a local keychain: when a
// distinct backup profile is configured and buildable, it wraps the primary in a
// fallback composite so failover behaves exactly as in-process. Every failure
// path returns the primary unchanged — a broken backup must never take down a
// working primary.
func wrapWorkerBackup(
	ctx context.Context,
	primary llm.Provider,
	primaryName string,
	cfg pkgcfg.Config,
	credSource credentialFetcher,
) llm.Provider {
	name := cfg.BackupCloudProfile
	if name == "" || name == primaryName {
		return primary
	}
	var bp pkgcfg.CloudProfile
	found := false
	for _, p := range cfg.CloudProfiles {
		if p.Name == name {
			bp = p
			found = true
			break
		}
	}
	if !found {
		log.Printf("[worker] backup profile %q not found; running without fallback", name)
		return primary
	}

	// Fetch the backup's credential via the stream (mirrors wrapBackup's
	// st.Get(bp.Name)) — for a ChatGPT-sub backup this is the access token, for
	// a static route the API key. The eager fetch gates the carve-out below for
	// ALL flavors, exactly like in-process; ChatGPT-sub still installs a lazy
	// TokenSource for per-call refresh.
	key := ""
	if k, _, err := credSource.Fetch(ctx, bp.Name); err == nil {
		key = k
	}
	// Same carve-out as in-process wrapBackup, applied to every flavor: no
	// credential (e.g. a logged-out ChatGPT-sub backup) + no BaseURL (proxy) +
	// not bedrock (AWS credential chain) → run without fallback rather than
	// wrapping an unusable backup.
	if key == "" && bp.BaseURL == "" && bp.Flavor != cloudfactory.FlavorBedrock {
		log.Printf("[worker] backup profile %q has no credential; running without fallback", name)
		return primary
	}
	var opts cloudfactory.Options
	if bp.Flavor == cloudfactory.FlavorResponses && bp.Route == cloudfactory.RouteChatGPT {
		// ChatGPT subscription: the host owns refresh/OAuth; the worker proxies
		// the token via the stream per call, keyed by the backup profile name.
		opts.TokenSource = &streamTokenSource{creds: credSource, profileName: bp.Name}
	}
	backup, buildErr := cloudfactory.BuildCloudProvider(bp, key, opts)
	if buildErr != nil {
		log.Printf("[worker] backup profile %q unbuildable (%v); running without fallback", name, buildErr)
		return primary
	}
	return fallback.New(primary, backup, bp.Model, func(stage string, ferr error) {
		log.Printf("[worker] failover to backup %q (%s): primary error: %v", name, stage, ferr)
	})
}

func (r *workerResolver) Main() (llm.Provider, bool, bool, error) {
	cfg := r.cfgSvc.Get()
	mode, _ := locus.ParseMode(cfg.LocusMode)
	sel, err := dispatch.Select(mode, dispatch.RoleMain, dispatch.Providers{
		Cloud: r.cloudProv,
		Open:  r.openProv,
	})
	if err != nil {
		return nil, false, false, err
	}
	return sel.Provider, sel.IsCloud, sel.FellBack, nil
}

func (r *workerResolver) MainModel(isCloud bool) string {
	c := r.cfgSvc.Get()
	if isCloud {
		// Mirror the host: resolve the everyday tier through the active profile's
		// vendor cost table, falling back to ActiveCloudModel when no active
		// profile is configured.
		if prof, ok := r.cfgSvc.ActiveProfile(); ok {
			return r.cfgSvc.Get().ModelProfiles.ResolveCloudModelForTier(prof, pkgcfg.TierEveryday)
		}
		return r.ActiveCloudModel()
	}
	// Open model lives in the everyday-open tier (OpenChatModel); the legacy
	// cfg.OpenModel field is blanked by config normalization. Mirrors the host.
	return (&c).OpenChatModel()
}

// PrimaryModel mirrors the host: for cloud-primary locus modes it resolves the
// everyday tier through the active profile's vendor cost table (falling back to
// ActiveCloudModel), otherwise the open model.
func (r *workerResolver) PrimaryModel() string {
	c := r.cfgSvc.Get()
	switch c.LocusMode {
	case "cloud_only", "cloud_primary":
		if prof, ok := r.cfgSvc.ActiveProfile(); ok {
			if m := c.ModelProfiles.ResolveCloudModelForTier(prof, pkgcfg.TierEveryday); m != "" {
				return m
			}
		}
		if m := r.ActiveCloudModel(); m != "" {
			return m
		}
	}
	return (&c).OpenChatModel()
}
func (r *workerResolver) Rebuild() error              { return nil }
func (r *workerResolver) InstallAbsentCloud(_ string) { r.cloudProv = nil }
func (r *workerResolver) Cloud() llm.Provider         { return r.cloudProv }
func (r *workerResolver) Open() llm.Provider          { return r.openProv }
func (r *workerResolver) ActiveCloudModel() string {
	if prof, ok := r.cfgSvc.ActiveProfile(); ok {
		return prof.Model
	}
	return r.cfgSvc.Get().CloudModel
}
func (r *workerResolver) LocusMode() string                                         { return r.cfgSvc.Get().LocusMode }
func (r *workerResolver) Router() providerssvc.RouterCloudUpdater                   { return nil }
func (r *workerResolver) Registry() *engine.EngineRegistry                          { return nil }
func (r *workerResolver) CatalogManager() *ollamacatalog.Manager                    { return nil }
func (r *workerResolver) OpenLegacy() *legacymodels.OpenModelProvider               { return nil }
func (r *workerResolver) SetCloudLLMProvider(p llm.Provider)                        { r.cloudProv = p }
func (r *workerResolver) SetOpenLLMProvider(p llm.Provider)                         { r.openProv = p }
func (r *workerResolver) SetOpenProviderFactory(_ func(pkgcfg.Config) llm.Provider) {}
func (r *workerResolver) CloudLLMProvider() llm.Provider                            { return r.cloudProv }
func (r *workerResolver) OpenLLMProvider() llm.Provider                             { return r.openProv }
func (r *workerResolver) Reconfigure(_ providerssvc.ReconfigureArgs)                {}
func (r *workerResolver) SetCatalogManager(_ *ollamacatalog.Manager)                {}
func (r *workerResolver) SetUsageSink(_ func(usage.Usage))                          {}

// ─── workerToolSvc ────────────────────────────────────────────────────────────

// workerToolSvc is a minimal runner.ToolSvc for the worker.
type workerToolSvc struct{ reg *agenttools.Registry }

func (s *workerToolSvc) Registry() *agenttools.Registry { return s.reg }
func (s *workerToolSvc) GrantedRegistry(tools []string) (*agenttools.Registry, []string, []string, error) {
	if s.reg == nil {
		return agenttools.NewRegistry(), nil, nil, nil
	}
	sub := agenttools.NewRegistry()
	var granted, unknown []string
	for _, name := range tools {
		if t, ok := s.reg.Get(name); ok {
			_ = sub.Register(t)
			granted = append(granted, name)
		} else {
			unknown = append(unknown, name)
		}
	}
	return sub, granted, unknown, nil
}

func buildWorkerTools() runner.ToolSvc {
	// Build the full capability + agent registry with all built-in tools.
	// MCP tools are NOT included — MCP servers run host-side only.
	capReg := capabilities.NewRegistry(capabilities.Services{})
	builtins.Register(capReg)
	reg := agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases(), builtins.CapabilitySynonyms())
	return &workerToolSvc{reg: reg}
}
