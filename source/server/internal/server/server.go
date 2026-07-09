package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/broker"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/compactiongen"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	persistsvc "cercano/source/server/internal/hostsvc/persistence"
	"cercano/source/server/internal/hostsvc/providers"
	runtimessvc "cercano/source/server/internal/hostsvc/runtimes"
	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/loop"
	mcphost "cercano/source/server/internal/mcp_host"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/protocols"
	"cercano/source/server/internal/retention"
	runnersvc "cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/sysram"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/watchdog"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// RouterCloudUpdater is the subset of the router interface the gRPC server
// needs to propagate a runtime cloud-provider swap. Both *agent.SmartRouter
// and *agent.LazyRouter satisfy this.
type RouterCloudUpdater interface {
	SetCloudProvider(p agent.ModelProvider)
	GetModelProviders() map[string]agent.ModelProvider
}

// McpManager is the subset of *mcphost.Manager the RPC handlers use. An
// interface so tests can inject a fake.
type McpManager interface {
	List() []mcphost.ServerStatus
	Add(ctx context.Context, name string, cfg mcphost.ServerConfig) error
	Remove(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
}

// Server is the gRPC server for the Agent service.
type Server struct {
	proto.UnimplementedAgentServer
	agent       *agent.Agent
	providerSvc providers.Resolver // owns cloud/open providers, router, coordinator, registry, catalogManager
	cfgSvc      cfgsvc.Service     // owns configPath, currentConfig, cfgMu, secrets
	toolSvc     toolssvc.Catalog   // owns toolRegistry, capRegistry, dispatchEngine
	persistSvc  persistsvc.Service // owns retentionSweeper, compactionGen, contextLoader
	permBroker  permissions.Broker
	runtimesSvc runtimessvc.Supervisors // owns meridianMgr, runtimeManager, mcpManager
	watchdog    *watchdog.Watchdog      // protocol-supervision gate; nil = disabled (default)
	// Two runners coexist so the front door can pick per turn. inProcessRunner is
	// always built (NewServer / SetPermissions) and is the embedded runnersvc.Core
	// the test suite constructs. workerRunner is nil until SelectExecutionMode
	// picks worker mode; when non-nil, a turn that touches NO host-side MCP tool
	// runs in a child process. MCP-involving turns fall back to inProcessRunner
	// (the worker excludes host-side MCP tools) — see hasMCPTools + the per-turn
	// pick in streamProcessRequestWithToolLoop.
	inProcessRunner runnersvc.TurnRunner // in-process turn execution; rebuilt when perms arrive
	workerRunner    runnersvc.TurnRunner // worker-process execution; nil unless worker mode selected

	events *eventHub // server->client push fan-out (SubscribeEvents)

	// turnBroker owns the per-conversation turn-exclusivity registry. A new
	// turn supersedes any prior turn on the same conversation: the prior ctx is
	// canceled and its generation retired so its persistence and event emission
	// become no-ops while it unwinds. Prevents two turns interleaving history
	// writes or sharing one upstream (Meridian) session key.
	turnBroker *broker.Broker
}

// beginTurn delegates to the turn broker. It registers a new turn for conv,
// superseding any turn already running there. Returns a ctx canceled when this
// turn is superseded or parent is done, this turn's generation, and a release
// func the caller must defer. Kept as a thin shim so existing call sites and
// server-package tests compile without change.
func (s *Server) beginTurn(parent context.Context, conv string) (context.Context, uint64, func()) {
	return s.turnBroker.BeginTurn(parent, conv)
}

// turnIsCurrent reports whether gen is still the live generation for conv.
// Delegator shim — logic lives in broker.Broker.IsCurrent.
func (s *Server) turnIsCurrent(conv string, gen uint64) bool {
	return s.turnBroker.IsCurrent(conv, gen)
}

// hasActiveTurn reports whether a turn is currently registered for conv.
// Delegator shim — logic lives in broker.Broker.HasActiveTurn.
func (s *Server) hasActiveTurn(conv string) bool {
	return s.turnBroker.HasActiveTurn(conv)
}

// SetContextLoader wires the project-context loader so the native tool-loop can
// include .cercano/context.md (and the working directory) in its system prompt.
func (s *Server) SetContextLoader(l *projectctx.Loader) { s.persistSvc.SetContextLoader(l) }

// SetDispatchEngine wires the unified dispatch engine so capability Services
// can dispatch one-shot co-processor work, and installs the agentic runner
// so Agentic dispatches can call agent.RunToolLoop without creating an import
// cycle between internal/dispatch and internal/agent.
// Call before InstallCapabilities.
func (s *Server) SetDispatchEngine(e *dispatch.Engine) {
	s.toolSvc.SetEngine(e)
}

// SetToolRegistry attaches the agent's tool registry. The CLI's /tools and
// /tool commands route through ListTools / InvokeTool RPCs to it.
func (s *Server) SetToolRegistry(r *agenttools.Registry) { s.toolSvc.SetRegistry(r) }

// ToolRegistry returns the current agent tool registry. Used by the MCP host
// to register dynamically connected tools into the same registry.
func (s *Server) ToolRegistry() *agenttools.Registry { return s.toolSvc.Registry() }

// InstallCapabilities builds the capability registry from the server's current
// providers, config, and context loader, then wires the resulting
// agenttools.Registry as the server's tool registry. Call AFTER
// SetCloudLLMProvider / SetOpenLLMProvider / SetContextLoader so that
// Services carries live runtime values.
func (s *Server) InstallCapabilities() {
	cfgSnapshot := s.cfgSvc.Get()
	capReg := capabilities.NewRegistry(capabilities.Services{
		CloudProvider: s.providerSvc.Cloud(),
		OpenProvider:  s.providerSvc.Open(),
		Config:        &cfgSnapshot,
		ProjectCtx:    s.persistSvc.ContextLoader(),
		// Engine/Conversations wired in a later phase; nil-safe until then.
		Dispatch: func(ctx context.Context, spec dispatch.Spec) (dispatch.Result, error) {
			e := s.toolSvc.Engine()
			if e == nil {
				return dispatch.Result{}, fmt.Errorf("dispatch engine not configured")
			}
			return e.Dispatch(ctx, spec)
		},
	})
	builtins.Register(capReg)
	s.toolSvc.SetCapRegistry(capReg)
	s.SetToolRegistry(agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases(), builtins.CapabilitySynonyms()))
}

// SetPermissions wires the permission store and pending-decisions barrier used
// by the SetPermissionMode / GetPermissionMode / Allow|DenyToolCall RPCs.
func (s *Server) SetPermissions(store *agent.PermissionStore, pending *agent.PendingDecisions) {
	s.permBroker = permissions.New(store, pending, func(mode string) {
		s.broadcastPermissionMode(mode)
	})
	// Forward the broker to the tools service so RunAgenticDispatch can use it.
	if s.toolSvc != nil {
		s.toolSvc.SetPermBroker(s.permBroker)
	}
	// Rebuild the in-process turn runner now that the permission broker is wired.
	s.inProcessRunner = runnersvc.New(s.runnerDeps())
}

// SetMcpManager wires the MCP host manager so the RPC handlers can delegate to it.
func (s *Server) SetMcpManager(m McpManager) {
	if s.runtimesSvc == nil {
		s.runtimesSvc = runtimessvc.New(nil)
	}
	s.runtimesSvc.SetMcpManager(m)
}

// mcpMgr returns the MCP manager from runtimesSvc, or nil when not configured.
func (s *Server) mcpMgr() McpManager {
	if s.runtimesSvc == nil {
		return nil
	}
	return s.runtimesSvc.McpManager()
}

// runtimeMgr returns the local runtime manager from runtimesSvc, or nil when not configured.
func (s *Server) runtimeMgr() localruntime.Manager {
	if s.runtimesSvc == nil {
		return nil
	}
	return s.runtimesSvc.RuntimeManager()
}

// ListMcpServers implements proto.AgentServer — returns a snapshot of all hosted MCP servers.
func (s *Server) ListMcpServers(ctx context.Context, _ *proto.ListMcpServersRequest) (*proto.ListMcpServersResponse, error) {
	out := &proto.ListMcpServersResponse{}
	mgr := s.mcpMgr()
	if mgr == nil {
		return out, nil
	}
	for _, st := range mgr.List() {
		out.Servers = append(out.Servers, &proto.McpServerInfo{
			Name: st.Name, State: string(st.State), ToolCount: int32(st.ToolCount), Error: st.Err,
		})
	}
	return out, nil
}

// AddMcpServer implements proto.AgentServer — connects a new MCP server and persists it.
func (s *Server) AddMcpServer(ctx context.Context, req *proto.AddMcpServerRequest) (*proto.AddMcpServerResponse, error) {
	mgr := s.mcpMgr()
	if mgr == nil {
		return &proto.AddMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	err := mgr.Add(ctx, req.GetName(), mcphost.ServerConfig{
		Command: req.GetCommand(), Args: req.GetArgs(), Env: req.GetEnv(),
	})
	if err != nil {
		return &proto.AddMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.AddMcpServerResponse{Ok: true}, nil
}

// RemoveMcpServer implements proto.AgentServer — stops an MCP server and removes it from config.
func (s *Server) RemoveMcpServer(ctx context.Context, req *proto.RemoveMcpServerRequest) (*proto.RemoveMcpServerResponse, error) {
	mgr := s.mcpMgr()
	if mgr == nil {
		return &proto.RemoveMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	if err := mgr.Remove(ctx, req.GetName()); err != nil {
		return &proto.RemoveMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.RemoveMcpServerResponse{Ok: true}, nil
}

// RestartMcpServer implements proto.AgentServer — tears down and reconnects a hosted MCP server.
func (s *Server) RestartMcpServer(ctx context.Context, req *proto.RestartMcpServerRequest) (*proto.RestartMcpServerResponse, error) {
	mgr := s.mcpMgr()
	if mgr == nil {
		return &proto.RestartMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	if err := mgr.Restart(ctx, req.GetName()); err != nil {
		return &proto.RestartMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	// Restart re-lists synchronously, so the post-restart tool count is
	// available immediately from the manager's status snapshot.
	var toolCount int32
	for _, st := range mgr.List() {
		if st.Name == req.GetName() {
			toolCount = int32(st.ToolCount)
			break
		}
	}
	return &proto.RestartMcpServerResponse{Ok: true, ToolCount: toolCount}, nil
}

// SetCloudLLMProvider attaches the native-tool-calling cloud provider used by
// GetProviderCapabilities. Optional — when nil, GetProviderCapabilities falls
// back to a hardcoded Anthropic-shaped capability snapshot.
func (s *Server) SetCloudLLMProvider(p llm.Provider) { s.providerSvc.SetCloudLLMProvider(p) }

// SetOpenLLMProvider attaches the native-tool-calling local provider (Ollama
// or the llama-server adapter, per open_runtime).
func (s *Server) SetOpenLLMProvider(p llm.Provider) { s.providerSvc.SetOpenLLMProvider(p) }

// SetOpenProviderFactory installs the constructor used to rebuild the native
// open provider when the local runtime selection changes at runtime (see the
// open_runtime branch in UpdateConfig).
func (s *Server) SetOpenProviderFactory(fn func(config.Config) llm.Provider) {
	s.providerSvc.SetOpenProviderFactory(fn)
}

// CloudLLMProvider / OpenLLMProvider return the RAW (unwrapped) providers. The
// dispatch engine reads these per-dispatch so a runtime cloud swap is honored,
// and wraps them itself for usage recording — so these must stay unwrapped.
func (s *Server) CloudLLMProvider() llm.Provider { return s.providerSvc.CloudLLMProvider() }
func (s *Server) OpenLLMProvider() llm.Provider  { return s.providerSvc.OpenLLMProvider() }

// SetUsageSink installs the sink that resolveMainProvider uses to wrap the
// main tool-loop's provider for token-usage recording. The server's stored
// providers stay raw; wrapping happens at hand-off so the dispatch engine can
// read raw providers without double-counting.
func (s *Server) SetUsageSink(fn func(usage.Usage)) {
	s.providerSvc.SetUsageSink(fn)
}

// SetSecrets attaches the secrets store used to retrieve profile API keys.
func (s *Server) SetSecrets(st secrets.Store) { s.cfgSvc.SetSecrets(st) }

// SetupMeridian constructs the local Meridian proxy manager and wires its
// status listener to the event hub so MeridianStatusChanged is broadcast on
// every state transition. logPath is the file Meridian's stdout/stderr is
// teed into (typically ~/.cercano/meridian.log).
//
// Call once at startup, after the event hub is initialised. After this,
// rebuildCloudLocked will Ensure/Stop the proxy as the active profile's
// route field changes.
func (s *Server) SetupMeridian(logPath string) {
	if s.runtimesSvc == nil {
		s.runtimesSvc = runtimessvc.New(s.cfgSvc)
	}
	s.runtimesSvc.SetupMeridian(logPath, s.broadcastMeridianStatus)
}

// Shutdown tears down long-lived subprocess managers the server owns
// (currently: Meridian). Safe to call once at process exit; cheap when
// nothing was started. Does NOT stop the gRPC server itself — that's the
// caller's job (cmd/cercano/main.go uses GracefulStop).
func (s *Server) Shutdown() {
	if s.runtimesSvc != nil {
		s.runtimesSvc.StopMeridian()
	}
	// Drain the per-conversation worker pool (kill warm workers + stop the
	// idle-reaper) when worker mode is armed. The in-process runner has nothing
	// to drain; the type assertion no-ops for it.
	if sd, ok := s.workerRunner.(interface{ Shutdown() }); ok {
		sd.Shutdown()
	}
}

// activeCloudModel returns the cloud model from the active profile — the
// authoritative request-time value. Falls back to the legacy CloudModel
// field only when no active profile exists (e.g. local-only configs). All
// code that asks "what cloud model are we using right now?" should go
// through cfgSvc.ActiveProfile(), not currentConfig.CloudModel directly.
func (s *Server) activeCloudModel() string {
	return s.providerSvc.ActiveCloudModel()
}

// activeProfile returns the configured active cloud profile, or false if none.
func (s *Server) activeProfile() (config.CloudProfile, bool) {
	return s.cfgSvc.ActiveProfile()
}

// profileByName looks up a profile by name.
func profileByName(profiles []config.CloudProfile, name string) (config.CloudProfile, bool) {
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	return config.CloudProfile{}, false
}

// persistConfig saves the current config to disk if a path is set.
func (s *Server) persistConfig() { s.cfgSvc.Persist() }

// installAbsentCloud clears the native cloud provider and points both the
// router and the coordinator's CloudModel at the absent sentinel, so a failed
// rebuild never leaves a half-wired cloud.
func (s *Server) installAbsentCloud(reason string) {
	s.providerSvc.InstallAbsentCloud(reason)
}

// rebuildCloud resolves the active profile + its key and rewires BOTH the native
// tool-loop cloud provider and the router/coordinator CloudModel. On any failure
// (no active profile, no key, unsupported flavor, keychain down) it clears the
// native cloud provider and installs the absent-cloud sentinel — the agent keeps
// running with cloud absent. Delegates to providerSvc.Rebuild().
func (s *Server) rebuildCloud() error {
	return s.providerSvc.Rebuild()
}

// rebuildCloudLocked is an alias for rebuildCloud retained for call-site
// compatibility. Lock-free: the providers service reads config via cfgSvc
// snapshots.
func (s *Server) rebuildCloudLocked() error {
	return s.providerSvc.Rebuild()
}

// RebuildCloud exports rebuildCloud for use by cmd/cercano/main.go at startup.
func (s *Server) RebuildCloud() error { return s.providerSvc.Rebuild() }

// GetCloudProfiles implements proto.AgentServer — returns the list of configured cloud profiles.
func (s *Server) GetCloudProfiles(ctx context.Context, req *proto.GetCloudProfilesRequest) (*proto.GetCloudProfilesResponse, error) {
	// Stored-key presence comes from the keychain key NAMES (a prompt-free
	// list) instead of reading each secret, which on macOS raises a Keychain
	// authorization prompt. Browsing the Cloud settings tab must never prompt.
	keyNamesForPresence := map[string]bool{}
	if st := s.cfgSvc.Secrets(); st != nil {
		if names, err := st.List(); err == nil {
			for _, n := range names {
				keyNamesForPresence[n] = true
			}
		}
	}
	cfg := s.cfgSvc.Get()
	active := cfg.ActiveCloudProfile
	profiles := cfg.CloudProfiles

	out := &proto.GetCloudProfilesResponse{Active: active}
	for _, p := range profiles {
		hasKey := keyNamesForPresence[p.Name]
		out.Profiles = append(out.Profiles, &proto.CloudProfileInfo{
			Name: p.Name, Flavor: p.Flavor, BaseUrl: p.BaseURL, Model: p.Model, HasKey: hasKey, Backend: p.Backend, Route: p.Route,
		})
	}
	if s.runtimesSvc != nil {
		if st, ok := s.runtimesSvc.MeridianStatus(); ok {
			out.MeridianStatus = meridianStatusToProto(st)
		} else {
			out.MeridianStatus = &proto.MeridianStatus{State: "disabled"}
		}
	} else {
		out.MeridianStatus = &proto.MeridianStatus{State: "disabled"}
	}
	return out, nil
}

// SetActiveCloudProfile implements proto.AgentServer — switches the active cloud profile.
func (s *Server) SetActiveCloudProfile(ctx context.Context, req *proto.SetActiveCloudProfileRequest) (*proto.SetActiveCloudProfileResponse, error) {
	// The client may have given up while this RPC sat in a queue (agent
	// restart, lock contention). Applying it anyway split-brains client and
	// server: the CLI reports failure while the switch silently lands. Bail
	// before mutating; a client that still wants the switch will re-send.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start := time.Now()
	defer func() {
		log.Printf("[cloud] SetActiveCloudProfile(%q) took %v", req.GetName(), time.Since(start).Round(time.Millisecond))
	}()
	if !s.cfgSvc.SetActiveProfile(req.GetName()) {
		return &proto.SetActiveCloudProfileResponse{Ok: false, Error: fmt.Sprintf("no profile %q", req.GetName())}, nil
	}
	if err := s.rebuildCloud(); err != nil {
		// active is set, but the provider couldn't be built — report it, keep going.
		s.persistConfig()
		return &proto.SetActiveCloudProfileResponse{Ok: false, Error: err.Error()}, nil
	}
	s.persistConfig()
	return &proto.SetActiveCloudProfileResponse{Ok: true}, nil
}

// SetCloudProfileKey implements proto.AgentServer — stores an API key for a profile.
func (s *Server) SetCloudProfileKey(ctx context.Context, req *proto.SetCloudProfileKeyRequest) (*proto.SetCloudProfileKeyResponse, error) {
	st := s.cfgSvc.Secrets()
	if st == nil {
		return &proto.SetCloudProfileKeyResponse{Ok: false, Error: "keychain unavailable"}, nil
	}
	exists, isActive := s.cfgSvc.ProfileInfo(req.GetName())
	if !exists {
		return &proto.SetCloudProfileKeyResponse{Ok: false, Error: fmt.Sprintf("no profile %q", req.GetName())}, nil
	}
	if err := st.Set(req.GetName(), req.GetApiKey()); err != nil {
		return &proto.SetCloudProfileKeyResponse{Ok: false, Error: err.Error()}, nil
	}
	// If the key belongs to the active profile, rebuild so it takes effect now.
	if isActive {
		_ = s.rebuildCloud()
	}
	return &proto.SetCloudProfileKeyResponse{Ok: true}, nil
}

// knownFlavor reports whether the flavor is a recognized enum value (whether or
// not it is implemented yet — coming-soon flavors are storable but won't activate).
func knownFlavor(f string) bool {
	switch f {
	case cloudfactory.FlavorMessages, cloudfactory.FlavorChatCompletions,
		cloudfactory.FlavorResponses, cloudfactory.FlavorBedrock:
		return true
	}
	return false
}

// UpsertCloudProfile implements proto.AgentServer — creates or updates a profile's
// metadata (the API key is managed separately via SetCloudProfileKey).
func (s *Server) UpsertCloudProfile(ctx context.Context, req *proto.UpsertCloudProfileRequest) (*proto.UpsertCloudProfileResponse, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return &proto.UpsertCloudProfileResponse{Ok: false, Error: "profile name is required"}, nil
	}
	if !knownFlavor(req.GetFlavor()) {
		return &proto.UpsertCloudProfileResponse{Ok: false, Error: fmt.Sprintf("unknown flavor %q", req.GetFlavor())}, nil
	}
	if req.GetFlavor() == cloudfactory.FlavorChatCompletions && strings.TrimSpace(req.GetBaseUrl()) == "" {
		return &proto.UpsertCloudProfileResponse{Ok: false, Error: "base_url is required for chat_completions"}, nil
	}
	np := config.CloudProfile{
		Name: name, Flavor: req.GetFlavor(), Backend: req.GetBackend(),
		BaseURL: req.GetBaseUrl(), Model: req.GetModel(), Route: req.GetRoute(),
	}
	// Preserve existing route/model when the request omits them (partial-metadata upsert).
	// We need to read first, then upsert with merged values.
	if existing, existsAlready := s.cfgSvc.ProfileInfo(name); existing {
		// Fetch the existing profile to preserve its route/model if not provided.
		cfg := s.cfgSvc.Get()
		for _, p := range cfg.CloudProfiles {
			if p.Name == name {
				if np.Route == "" {
					np.Route = p.Route
				}
				if np.Model == "" {
					np.Model = p.Model
				}
				break
			}
		}
		_ = existsAlready // used below via cfgSvc.UpsertProfile return
	}
	_, isActive := s.cfgSvc.UpsertProfile(np)
	// If this is the active profile, rebuild so metadata changes take effect
	// now, and broadcast the model so client chrome (header chip) updates live.
	if isActive {
		if err := s.rebuildCloud(); err != nil {
			// active is set, but the provider couldn't be built — report it, keep going.
			s.persistConfig()
			return &proto.UpsertCloudProfileResponse{Ok: false, Error: err.Error()}, nil
		}
		s.broadcastConfigChanged("cloud_model", np.Model)
	}
	s.persistConfig()
	return &proto.UpsertCloudProfileResponse{Ok: true}, nil
}

// RemoveCloudProfile implements proto.AgentServer — deletes a profile and its
// keychain key. Clears the active profile (→ absent cloud) if it was active.
func (s *Server) RemoveCloudProfile(ctx context.Context, req *proto.RemoveCloudProfileRequest) (*proto.RemoveCloudProfileResponse, error) {
	name := req.GetName()
	existed, wasActive := s.cfgSvc.RemoveProfile(name)
	if !existed {
		return &proto.RemoveCloudProfileResponse{Ok: false, Error: fmt.Sprintf("no profile %q", name)}, nil
	}

	if st := s.cfgSvc.Secrets(); st != nil {
		_ = st.Delete(name) // best-effort; missing key is not an error
	}
	if wasActive {
		s.installAbsentCloud("active cloud profile removed")
	}
	s.persistConfig()
	return &proto.RemoveCloudProfileResponse{Ok: true}, nil
}

// resolveMainProvider picks the llm.Provider for the main tool-loop per the
// active Locus Mode. Returns the provider, whether it's the cloud tier, whether
// this is a fallback (preferred tier unavailable), or an error when the mode
// forbids crossing and the required tier has no provider wired.
// Delegates to providerSvc.Main().
func (s *Server) resolveMainProvider() (llm.Provider, bool, bool, error) {
	return s.providerSvc.Main()
}

// SetRuntimeManager attaches the local runtime/dashboard state manager.
func (s *Server) SetRuntimeManager(m localruntime.Manager) {
	if s.runtimesSvc == nil {
		s.runtimesSvc = runtimessvc.New(s.cfgSvc)
	}
	s.runtimesSvc.SetRuntimeManager(m)
}

// SetCatalogManager attaches the online-catalog manager so
// ListRuntimeModels can surface Ollama's public library alongside the
// hardcoded catalog + downloaded files, and RefreshOnlineCatalog is
// wired to the manager's Refresh method.
func (s *Server) SetCatalogManager(cm *ollamacatalog.Manager) {
	s.providerSvc.SetCatalogManager(cm)
}

// SetRetentionSweeper attaches the background retention sweeper so that
// /config and settings-page changes to retention horizons take effect on the
// next sweep without a restart.
func (s *Server) SetRetentionSweeper(sw *retention.Sweeper) { s.persistSvc.SetRetentionSweeper(sw) }

// SetCompactionGenerator attaches the background compaction scheduler so that
// /config compaction-enabled true|false flips it at runtime without a restart.
func (s *Server) SetCompactionGenerator(g *compactiongen.Generator) {
	s.persistSvc.SetCompactionGenerator(g)
}

// NewServer creates a new Agent gRPC server.
func NewServer(a *agent.Agent, openProvider *legacymodels.OpenModelProvider, router RouterCloudUpdater, coordinator *loop.ADKCoordinator, cloudFactory agent.CloudFactory, registry *engine.EngineRegistry) *Server {
	cfgService := cfgsvc.New("", config.Config{}, nil)
	rtSvc := runtimessvc.New(cfgService)
	s := &Server{
		agent:       a,
		events:      newEventHub(),
		cfgSvc:      cfgService,
		runtimesSvc: rtSvc,
		turnBroker:  broker.New(),
	}
	// syncMeridian bridges rebuildCloud → runtimesSvc.SyncMeridianForProfile
	// without the providers service holding a direct meridianMgr reference.
	// The callback captures rtSvc (constructed above and stored on s) so it
	// can reach the manager at call time (set after construction via SetupMeridian).
	syncMeridianFn := func(p config.CloudProfile, _ config.Config) {
		rtSvc.SyncMeridianForProfile(p)
	}
	s.providerSvc = providers.New(cfgService, openProvider, router, coordinator, cloudFactory, registry, syncMeridianFn, nil)
	// Construct the persistence service. It wraps the agent for store access;
	// the agent itself is NOT owned by this service. The func-value collaborators
	// read live state from providerSvc at call time.
	s.persistSvc = persistsvc.New(
		a, // ConvAgent — wraps *agent.Agent; nil-safe in all service methods
		cfgService,
		func() string { return s.providerSvc.PrimaryModel() },
		func() string { return s.activeCloudModel() },
		func() *dispatch.Engine { return s.toolSvc.Engine() },
		func() *legacymodels.OpenModelProvider { return s.providerSvc.OpenLegacy() },
		func() llm.Provider { return s.providerSvc.Cloud() },
		func() string { return s.activeCloudModel() },
	)
	// Construct the tool catalog service. permBroker is not yet wired here
	// (SetPermissions is called by the caller after construction), so it is
	// passed nil and updated via toolSvc.SetPermBroker in SetPermissions.
	// The store and persistTurn func-values are now sourced from persistSvc.
	s.toolSvc = toolssvc.New(
		nil, // permBroker — wired in SetPermissions
		s.buildSystemPrompt,
		func() conversation.Store { return s.persistSvc.Store() },
		s.persistSvc.PersistTurn,
	)
	// Wire the in-process turn runner with nil Perms (permBroker not yet set).
	// Rebuilt in SetPermissions once the broker is wired. workerRunner stays nil
	// until SelectExecutionMode picks worker mode, so a Server built directly (the
	// test suite) always runs turns in-process and never spawns a worker.
	s.inProcessRunner = runnersvc.New(s.runnerDeps())
	return s
}

// runnerDeps builds the runner.Deps snapshot from current server state.
// Called at NewServer time and again when SetPermissions wires the broker.
func (s *Server) runnerDeps() runnersvc.Deps {
	return runnersvc.Deps{
		Providers: s.providerSvc,
		Tools:     s.toolSvc,
		Persist:   s.persistSvc,
		Config:    s.cfgSvc,
		Perms:     s.permBroker,
		Agent:     s.agent,
		// Live accessor: s.watchdog is wired by InitWatchdog and mutated by
		// UpdateConfig AFTER this runner is built, so the runner reads it at
		// turn time rather than capturing the (often still-nil) current value.
		Watchdog: func() *watchdog.Watchdog { return s.watchdog },
	}
}

// SelectExecutionMode chooses which turn runners are available based on the
// live config's ExecutionMode. It is the seam that arms crash isolation:
//
//   - "in_process" → leave workerRunner nil, so every turn runs on the embedded
//     in-process runnersvc.Core (fast, what the test suite constructs). A Server
//     that never calls this method (the hermetic suite) stays in-process and
//     never spawns a worker process.
//   - anything else, including the production default "worker" and empty →
//     build the worker.NewWorkerRunner (child-process execution) and hold it
//     ALONGSIDE the in-process runner. The front door then picks per turn:
//     turns that touch no host-side MCP tool run in the worker (a crash takes
//     down only that turn's process; the host survives — see
//     TestWorker_CrashMidTurnIsIsolated); MCP-involving turns fall back to
//     in-process because the worker excludes host-side MCP tools.
//
// The per-turn pick (not a startup registry snapshot) is deliberate: MCP servers
// connect in the BACKGROUND after this method runs, and AddMcpServer can register
// tools at runtime — so a one-time check here would see zero MCP tools even when
// servers are configured. hasMCPTools reads the live registry at turn time.
//
// Called from cmd/cercano/main.go's server wiring AFTER the real config,
// permissions, and secrets are injected — so production arms worker mode while
// the hermetic test suite keeps the in-process default untouched.
func (s *Server) SelectExecutionMode() {
	mode := s.cfgSvc.Get().ExecutionMode
	if mode == "in_process" {
		// Explicit embedded/test mode: no worker runner; all turns in-process.
		s.workerRunner = nil
		return
	}
	// Production default ("worker" or empty): arm worker-process execution. The
	// in-process runner stays as the fallback for MCP-involving turns.
	s.workerRunner = worker.NewWorkerRunner(
		s.persistSvc,       // pre-assembles history + project context
		s.cfgSvc,           // builds the ConfigSnapshot
		s.permBroker,       // permission mode + decisions
		s.cfgSvc.Secrets(), // resolves credentials for the worker's CredentialRequests
	)
	log.Printf("[server] execution mode: worker (turns run in isolated child processes; " +
		"MCP-involving turns fall back to in-process — worker MCP proxying is a future refinement)")
}

// pickTurnRunner chooses the runner for THIS turn. Default: the in-process
// runner. Use the worker only when worker mode is armed (workerRunner != nil)
// AND the turn touches no host-side MCP tool — the worker excludes host-side
// MCP tools, so an MCP-involving turn must run in-process to keep them. One
// if-check on the hot path; no logging (the mode was logged once at selection).
func (s *Server) pickTurnRunner() runnersvc.TurnRunner {
	if s.workerRunner != nil && !s.hasMCPTools() {
		return s.workerRunner
	}
	return s.inProcessRunner
}

// hasMCPTools reports whether the host tool registry currently holds any
// MCP-origin tool. Called per turn to route MCP-involving turns to the
// in-process runner (the worker excludes host-side MCP tools). The registry read
// is cheap and concurrency-safe (agenttools.Registry guards All() with an
// RWMutex), so a concurrent AddMcpServer at worst yields a momentarily stale
// answer — self-correcting on the next turn.
func (s *Server) hasMCPTools() bool {
	if s.toolSvc == nil || s.toolSvc.Registry() == nil {
		return false
	}
	for _, t := range s.toolSvc.Registry().All() {
		if agenttools.OriginOf(t) == agenttools.OriginMCP {
			return true
		}
	}
	return false
}

// SetConfigPersistence enables config persistence by storing the config path and current state.
func (s *Server) SetConfigPersistence(path string, cfg config.Config) {
	s.cfgSvc.SetPath(path)
	s.cfgSvc.Set(cfg)
	if s.runtimesSvc != nil {
		s.runtimesSvc.RefreshRuntimeEndpoints()
	}
}

// LocusMode returns the currently configured Locus Mode (live; reflects
// UpdateConfig). Used by the agent for co-processor tier resolution.
func (s *Server) LocusMode() string {
	return s.providerSvc.LocusMode()
}

// UpdateConfig implements proto.AgentServer — updates runtime config without restart.
//
// Split: the config service owns parse→validate→mutate→persist; the
// provider/runtime-facing block (health-monitor restart, openProvider.SetEngine,
// openProviderFactory) stays on the front door and runs AFTER cfgSvc is
// updated. Config mutation and provider reconfiguration are now sequential
// (previously atomic under one cfgMu span) — a concurrent reader can momentarily
// observe new config with old provider wiring (documented trade-off).
func (s *Server) UpdateConfig(ctx context.Context, req *proto.UpdateConfigRequest) (*proto.UpdateConfigResponse, error) {
	// Take a snapshot to work from. Mutations accumulate into this local copy,
	// then cfgSvc.Set(c) commits everything atomically at the end. This keeps
	// each individual step free of cfgMu (cfgSvc's internal lock is fine-grained).
	c := s.cfgSvc.Get()

	var changes []string

	if req.OllamaUrl != "" {
		u, err := url.ParseRequestURI(req.OllamaUrl)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid ollama_url %q: must be a valid http:// or https:// URL", req.OllamaUrl),
			}, nil
		}
		// Provider block handled by providerSvc.Reconfigure (called below after
		// all mutations are assembled).
		changes = append(changes, fmt.Sprintf("ollama_url=%s", req.OllamaUrl))
		fmt.Printf("UpdateConfig: Ollama URL set to %s (health monitor started)\n", req.OllamaUrl)
	}

	if req.OpenModel != "" {
		// Provider block handled by providerSvc.Reconfigure (called below after
		// all mutations are assembled).
		changes = append(changes, fmt.Sprintf("local_model=%s", req.OpenModel))
		fmt.Printf("UpdateConfig: Local model set to %s\n", req.OpenModel)
	}

	if req.OpenDefaultModel != "" {
		// Applied before the open_runtime switch below so a request carrying
		// both resolves an ambiguous-model detection in one round trip —
		// detection and the engine-model pick both read LlamaServer.DefaultModel.
		c.LlamaServer.DefaultModel = req.OpenDefaultModel
		changes = append(changes, fmt.Sprintf("open_default_model=%s", req.OpenDefaultModel))
		fmt.Printf("UpdateConfig: llama-server default model set to %s\n", req.OpenDefaultModel)
	}

	if req.OpenRuntime != "" {
		if req.OpenRuntime != "ollama" && req.OpenRuntime != "llama_server" {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid local_runtime %q: expected ollama or llama_server", req.OpenRuntime),
			}, nil
		}
		if s.providerSvc.Registry() == nil {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: "engine registry is not configured",
			}, nil
		}
		if _, err := s.providerSvc.Registry().GetEngine(req.OpenRuntime); err != nil {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("local runtime %q is not available: %v", req.OpenRuntime, err),
			}, nil
		}
		// Runtime-swap auto-configure: for llama_server, run headless detection
		// so an edit like `local_runtime: llama_server` in config.yaml
		// populates Binary + DefaultModel from the environment (PATH lookup +
		// GGUF scan) instead of silently landing a swap that fails on the next
		// inference call. The swap itself proceeds either way — a failed
		// detection lands as a OpenRuntimeStatusChanged{ok=false} event so
		// the CLI can offer the install/model-picker flow.
		var detectErr *llamaserver.DetectError
		if req.OpenRuntime == "llama_server" {
			if err := llamaserver.Detect(ctx, &c.LlamaServer); err != nil {
				if de, ok := err.(*llamaserver.DetectError); ok {
					detectErr = de
				}
				fmt.Printf("UpdateConfig: llama-server detection: %v\n", err)
			} else {
				fmt.Printf("UpdateConfig: llama-server auto-configured — binary=%s default_model=%s\n",
					c.LlamaServer.Binary, c.LlamaServer.DefaultModel)
			}
		}
		// Provider block handled by providerSvc.Reconfigure (called below after
		// all mutations are assembled). The resolved model is computed here and
		// passed via ReconfigureArgs so Reconfigure doesn't re-detect.
		changes = append(changes, fmt.Sprintf("local_runtime=%s", req.OpenRuntime))
		fmt.Printf("UpdateConfig: Local runtime set to %s\n", req.OpenRuntime)
		s.broadcastOpenRuntimeStatus(buildOpenRuntimeStatus(req.OpenRuntime, c, detectErr))
	}

	if req.LocusMode != "" {
		if _, err := locus.ParseMode(req.LocusMode); err != nil {
			return &proto.UpdateConfigResponse{Success: false, Message: err.Error()}, nil
		}
		changes = append(changes, fmt.Sprintf("locus_mode=%s", req.LocusMode))
		fmt.Printf("UpdateConfig: Locus mode set to %s\n", req.LocusMode)
	}

	if req.ElideToolResults != "" {
		v := strings.ToLower(strings.TrimSpace(req.ElideToolResults))
		if v != "true" && v != "false" {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid elide_tool_results %q: expected \"true\" or \"false\"", req.ElideToolResults),
			}, nil
		}
		c.Compaction.ElideToolResults = v == "true"
		changes = append(changes, fmt.Sprintf("elide_tool_results=%s", v))
		s.broadcastConfigChanged("elide_tool_results", v)
		fmt.Printf("UpdateConfig: elide_tool_results set to %s\n", v)
	}

	if req.LossyToolElision != "" {
		v := strings.ToLower(strings.TrimSpace(req.LossyToolElision))
		if v != "true" && v != "false" {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid lossy_tool_elision %q: expected \"true\" or \"false\"", req.LossyToolElision),
			}, nil
		}
		c.Compaction.LossyToolElision = v == "true"
		changes = append(changes, fmt.Sprintf("lossy_tool_elision=%s", v))
		s.broadcastConfigChanged("lossy_tool_elision", v)
		fmt.Printf("UpdateConfig: lossy_tool_elision set to %s\n", v)
	}

	if req.CompactionEnabled != "" {
		v := strings.ToLower(strings.TrimSpace(req.CompactionEnabled))
		if v != "true" && v != "false" {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid compaction_enabled %q: expected \"true\" or \"false\"", req.CompactionEnabled),
			}, nil
		}
		enabled := v == "true"
		c.Compaction.Enabled = enabled
		// Flip the runtime kill switch. In-flight passes finish; new Schedule
		// calls noop when disabled. Nil-guarded because the server may run
		// without a persistent store (no compGen wired).
		s.persistSvc.SetCompactionEnabled(enabled)
		changes = append(changes, fmt.Sprintf("compaction_enabled=%s", v))
		s.broadcastConfigChanged("compaction_enabled", v)
		fmt.Printf("UpdateConfig: compaction_enabled set to %s\n", v)
	}

	retentionChanged := false
	if req.RawRetentionDays != "" {
		n, err := strconv.Atoi(strings.TrimSpace(req.RawRetentionDays))
		if err != nil || n < 0 {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid raw_retention_days %q: expected a non-negative integer", req.RawRetentionDays),
			}, nil
		}
		c.Compaction.Retention.RawRetentionDays = n
		changes = append(changes, fmt.Sprintf("raw_retention_days=%d", n))
		s.broadcastConfigChanged("raw_retention_days", strconv.Itoa(n))
		fmt.Printf("UpdateConfig: raw_retention_days set to %d\n", n)
		retentionChanged = true
	}
	if req.CompactedRetentionDays != "" {
		n, err := strconv.Atoi(strings.TrimSpace(req.CompactedRetentionDays))
		if err != nil || n < 0 {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid compacted_retention_days %q: expected a non-negative integer", req.CompactedRetentionDays),
			}, nil
		}
		c.Compaction.Retention.CompactedRetentionDays = n
		changes = append(changes, fmt.Sprintf("compacted_retention_days=%d", n))
		s.broadcastConfigChanged("compacted_retention_days", strconv.Itoa(n))
		fmt.Printf("UpdateConfig: compacted_retention_days set to %d\n", n)
		retentionChanged = true
	}
	if req.KeepForever != "" {
		v := strings.ToLower(strings.TrimSpace(req.KeepForever))
		if v != "true" && v != "false" {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid keep_forever %q: expected \"true\" or \"false\"", req.KeepForever),
			}, nil
		}
		c.Compaction.Retention.KeepForever = v == "true"
		changes = append(changes, fmt.Sprintf("keep_forever=%s", v))
		s.broadcastConfigChanged("keep_forever", v)
		fmt.Printf("UpdateConfig: keep_forever set to %s\n", v)
		retentionChanged = true
	}
	// Push the reconciled retention block to the background sweeper so the
	// next sweep uses the new horizons without waiting for a restart.
	if retentionChanged {
		r := c.Compaction.Retention
		s.persistSvc.UpdateRetentionConfig(retention.Config{
			RawRetentionDays:       r.RawRetentionDays,
			CompactedRetentionDays: r.CompactedRetentionDays,
			KeepForever:            r.KeepForever,
		})
	}

	// Cloud changes go through the active profile + rebuildCloud(). The
	// profile is the single source of truth (see activeCloudModel); writing
	// req.CloudModel anywhere else just creates the kind of split-state bug
	// where the displayed/persisted model and the actually-used model
	// disagree. cloud_provider is treated as a legacy display field with no
	// effect on routing (flavor on the profile is what matters at request
	// time).
	wantCloudRebuild := req.CloudProvider != "" || req.CloudModel != "" || req.CloudApiKey != "" || req.CloudBaseUrl != ""
	if wantCloudRebuild {
		// Mutate the active profile in the local snapshot, commit it to cfgSvc,
		// then drive rebuildCloudLocked (which reads a fresh snapshot from cfgSvc).
		activeName := c.ActiveCloudProfile
		idx := -1
		for i, p := range c.CloudProfiles {
			if p.Name == activeName {
				idx = i
				break
			}
		}
		if idx < 0 {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: "no active cloud profile — create one via SetCloudProfile / SetActiveCloudProfile before /config can update cloud fields",
			}, nil
		}
		if req.CloudModel != "" {
			c.CloudProfiles[idx].Model = req.CloudModel
		}
		if req.CloudBaseUrl != "" {
			c.CloudProfiles[idx].BaseURL = req.CloudBaseUrl
		}
		profileName := c.CloudProfiles[idx].Name

		// API key goes to the keychain (keyed by profile name), never to
		// the profile struct or the legacy CloudAPIKey field.
		if req.CloudApiKey != "" {
			if st := s.cfgSvc.Secrets(); st != nil {
				if err := st.Set(profileName, req.CloudApiKey); err != nil {
					return &proto.UpdateConfigResponse{
						Success: false,
						Message: fmt.Sprintf("failed to store API key: %v", err),
					}, nil
				}
			}
		}

		// Commit the profile mutations before rebuilding so rebuildCloudLocked
		// reads the updated profile from cfgSvc.
		s.cfgSvc.Set(c)
		c = s.cfgSvc.Get() // re-snapshot so subsequent reads are consistent

		if err := s.rebuildCloudLocked(); err != nil {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("failed to rebuild cloud provider: %v", err),
			}, nil
		}
		summary := profileName
		if req.CloudModel != "" {
			summary += "/" + req.CloudModel
		}
		changes = append(changes, "cloud="+summary)
		fmt.Printf("UpdateConfig: Cloud profile %q rebuilt\n", profileName)
	}

	watchdogChanged := false
	if req.WatchdogEnabled != "" {
		c.Watchdog.Enabled = req.WatchdogEnabled == "true"
		changes = append(changes, fmt.Sprintf("watchdog_enabled=%s", req.WatchdogEnabled))
		watchdogChanged = true
	}
	if req.WatchdogEcho != "" {
		c.Watchdog.Echo = req.WatchdogEcho == "true"
		changes = append(changes, fmt.Sprintf("watchdog_echo=%s", req.WatchdogEcho))
		watchdogChanged = true
	}
	if req.WatchdogMode == "challenge-and-justify" || req.WatchdogMode == "strict" {
		c.Watchdog.Mode = req.WatchdogMode
		changes = append(changes, "watchdog_mode="+req.WatchdogMode)
		watchdogChanged = true
	}
	if req.WatchdogEscalateAfter != "" {
		if n, err := strconv.Atoi(req.WatchdogEscalateAfter); err == nil && n >= 1 {
			c.Watchdog.EscalateAfter = n
			changes = append(changes, fmt.Sprintf("watchdog_escalate_after=%d", n))
			watchdogChanged = true
		}
	}
	if req.WatchdogChecks != "" {
		if req.WatchdogChecks == "-" {
			c.Watchdog.Checks = []string{}
		} else {
			parts := strings.Split(req.WatchdogChecks, ",")
			checks := make([]string, 0, len(parts))
			for _, p := range parts {
				if trimmed := strings.TrimSpace(p); trimmed != "" {
					checks = append(checks, trimmed)
				}
			}
			c.Watchdog.Checks = checks
		}
		changes = append(changes, "watchdog_checks="+req.WatchdogChecks)
		watchdogChanged = true
	}
	if req.ModelTierKey == "embedding.open" {
		// The embedding slot is tier UI over the real embedding_model
		// config field — NOT a Models-taxonomy entry — so there's a
		// single source of truth. The embedding engine is built at
		// startup, so the change persists now and applies on restart.
		val := strings.TrimSpace(req.ModelTierValue)
		if val == "-" {
			val = ""
		}
		c.Models.Tiers.Embedding.Open = val
		desc := "embedding_model=" + val
		if val == "" {
			desc = "embedding_model unset"
		}
		changes = append(changes, desc+" (takes effect on restart)")
		s.broadcastConfigChanged("embedding_model", val)
	} else if req.ModelTierKey != "" {
		desc, err := config.ApplyModelTierPatch(&c.Models, req.ModelTierKey, req.ModelTierValue)
		if err != nil {
			return &proto.UpdateConfigResponse{Success: false, Message: err.Error()}, nil
		}
		changes = append(changes, desc)
		s.broadcastConfigChanged("models."+req.ModelTierKey, req.ModelTierValue)
		// The watchdog resolves its one-shot model from the taxonomy at build
		// time, so a tier change must rebuild it to take effect.
		watchdogChanged = true
	}
	if watchdogChanged {
		// Rebuild the supervisor from the just-mutated local config snapshot.
		s.watchdog = s.buildWatchdogFrom(c.Watchdog, c.Models)
	}

	if len(changes) == 0 {
		return &proto.UpdateConfigResponse{
			Success: true,
			Message: "no changes requested",
		}, nil
	}

	if req.OllamaUrl != "" {
		c.OllamaURL = req.OllamaUrl
		s.broadcastConfigChanged("ollama_url", req.OllamaUrl)
	}
	if req.OpenModel != "" {
		c.Models.Tiers.Everyday.Open = req.OpenModel
		s.broadcastConfigChanged("local_model", req.OpenModel)
	}
	if req.OpenRuntime != "" {
		c.OpenRuntime = req.OpenRuntime
		s.broadcastConfigChanged("local_runtime", req.OpenRuntime)
	}
	if req.OpenDefaultModel != "" {
		// c.LlamaServer.DefaultModel was already set up top (before the runtime
		// switch) — only the broadcast belongs here.
		s.broadcastConfigChanged("open_default_model", req.OpenDefaultModel)
	}
	if req.CloudProvider != "" {
		c.CloudProvider = req.CloudProvider
		s.broadcastConfigChanged("cloud_provider", req.CloudProvider)
	}
	if req.CloudModel != "" {
		c.CloudModel = req.CloudModel
		s.broadcastConfigChanged("cloud_model", req.CloudModel)
	}
	if req.CloudApiKey != "" {
		c.CloudAPIKey = req.CloudApiKey
		// Presence marker only — never broadcast a raw secret.
		s.broadcastConfigChanged("cloud_api_key", "set")
	}
	if req.CloudBaseUrl != "" {
		c.CloudBaseURL = req.CloudBaseUrl
		s.broadcastConfigChanged("cloud_base_url", req.CloudBaseUrl)
	}
	if req.LocusMode != "" {
		c.LocusMode = req.LocusMode
		s.broadcastConfigChanged("locus_mode", req.LocusMode)
	}

	// Commit all config mutations to the service and persist.
	s.cfgSvc.Set(c)
	s.applyRuntimeEndpoints(c)
	s.cfgSvc.Persist()

	// Apply provider/runtime mutations. This runs after cfgSvc.Set so
	// Reconfigure reads the fully-committed config when rebuilding providers.
	// The resolved open model for a runtime swap is computed above; we pass the
	// fully-mutated snapshot so the factory can rebuild with the new runtime.
	if req.OllamaUrl != "" || req.OpenModel != "" || req.OpenRuntime != "" {
		resolvedModel := req.OpenModel
		if resolvedModel == "" && req.OpenRuntime == "llama_server" {
			resolvedModel = c.LlamaServer.DefaultModel
		}
		if resolvedModel == "" && req.OpenRuntime != "" {
			resolvedModel = (&c).OpenChatModel()
		}
		s.providerSvc.Reconfigure(providers.ReconfigureArgs{
			OllamaURL:         req.OllamaUrl,
			OpenModel:         req.OpenModel,
			OpenRuntime:       req.OpenRuntime,
			ResolvedOpenModel: resolvedModel,
			MutatedConfig:     c,
		})
	}

	return &proto.UpdateConfigResponse{
		Success: true,
		Message: fmt.Sprintf("updated: [%s]", strings.Join(changes, ", ")),
	}, nil
}

// ListConversations implements proto.AgentServer — delegates to persistSvc.
func (s *Server) ListConversations(ctx context.Context, req *proto.ListConversationsRequest) (*proto.ListConversationsResponse, error) {
	return s.persistSvc.ListConversations(ctx, req)
}

// GetConversation implements proto.AgentServer — delegates to persistSvc.
func (s *Server) GetConversation(ctx context.Context, req *proto.GetConversationRequest) (*proto.Conversation, error) {
	return s.persistSvc.GetConversation(ctx, req)
}

// ResumeConversation implements proto.AgentServer — delegates to persistSvc.
func (s *Server) ResumeConversation(ctx context.Context, req *proto.ResumeConversationRequest) (*proto.ResumeConversationResponse, error) {
	return s.persistSvc.ResumeConversation(ctx, req)
}

// DeleteConversation implements proto.AgentServer — delegates to persistSvc.
func (s *Server) DeleteConversation(ctx context.Context, req *proto.DeleteConversationRequest) (*proto.DeleteConversationResponse, error) {
	return s.persistSvc.DeleteConversation(ctx, req)
}

// RenameConversation implements proto.AgentServer — delegates to persistSvc.
func (s *Server) RenameConversation(ctx context.Context, req *proto.RenameConversationRequest) (*proto.RenameConversationResponse, error) {
	return s.persistSvc.RenameConversation(ctx, req)
}

// ListTools implements proto.AgentServer — enumerates the agent's tool
// registry for the CLI's /tools listing. Returns an empty list when no
// registry was wired (e.g. tests that don't need tools).
func (s *Server) ListTools(ctx context.Context, req *proto.ListToolsRequest) (*proto.ListToolsResponse, error) {
	reg := s.toolSvc.Registry()
	if reg == nil {
		return &proto.ListToolsResponse{}, nil
	}
	tools := reg.All()
	out := &proto.ListToolsResponse{Tools: make([]*proto.BuiltinTool, 0, len(tools))}
	for _, t := range tools {
		out.Tools = append(out.Tools, &proto.BuiltinTool{
			Name:        t.Name(),
			Description: t.Description(),
			Permission:  string(t.Permission()),
			Schema:      string(t.Schema()),
			Destructive: agenttools.IsDestructive(t),
		})
	}
	return out, nil
}

// InvokeTool implements proto.AgentServer — runs the named tool with JSON
// args. Tool errors are surfaced as InvokeToolResponse.error rather than gRPC
// errors so the CLI can render them inline.
func (s *Server) InvokeTool(ctx context.Context, req *proto.InvokeToolRequest) (*proto.InvokeToolResponse, error) {
	resp := &proto.InvokeToolResponse{}
	reg := s.toolSvc.Registry()
	if reg == nil {
		resp.Error = "no tool registry configured"
		return resp, nil
	}
	tool, ok := reg.Get(req.GetName())
	if !ok {
		resp.Error = fmt.Sprintf("unknown tool %q", req.GetName())
		return resp, nil
	}
	argsJSON := req.GetArgsJson()
	if argsJSON == "" {
		argsJSON = "{}"
	}
	result, err := tool.Execute(ctx, []byte(argsJSON))
	if err != nil {
		resp.Error = err.Error()
		return resp, nil
	}
	resp.ResultType = string(result.Type)
	resp.Truncated = result.Truncated
	resp.Note = result.Note
	switch result.Type {
	case agenttools.ResultText:
		resp.Text = result.Text
	case agenttools.ResultRows:
		b, err := json.Marshal(result.Rows)
		if err != nil {
			resp.Error = "marshal rows: " + err.Error()
			return resp, nil
		}
		resp.RowsJson = string(b)
	case agenttools.ResultJSON:
		resp.Json = string(result.JSON)
	}
	return resp, nil
}

// GetContextUsage implements proto.AgentServer — delegates to persistSvc.
func (s *Server) GetContextUsage(ctx context.Context, req *proto.GetContextUsageRequest) (*proto.GetContextUsageResponse, error) {
	return s.persistSvc.GetContextUsage(ctx, req)
}

// SuggestNextPrompt implements proto.AgentServer — delegates to persistSvc.
func (s *Server) SuggestNextPrompt(ctx context.Context, req *proto.SuggestNextPromptRequest) (*proto.SuggestNextPromptResponse, error) {
	return s.persistSvc.SuggestNextPrompt(ctx, req)
}

// sanitizeSuggestion is a test shim — the canonical implementation lives in
// hostsvc/persistence as SanitizeSuggestion. Tests in this package call
// sanitizeSuggestion (unexported); this delegates so they keep compiling.
func sanitizeSuggestion(s string) string { return persistsvc.SanitizeSuggestion(s) }

// GetCompactionState implements proto.AgentServer — delegates to persistSvc.
func (s *Server) GetCompactionState(ctx context.Context, req *proto.GetCompactionStateRequest) (*proto.GetCompactionStateResponse, error) {
	return s.persistSvc.GetCompactionState(ctx, req)
}

// ExportContext implements proto.AgentServer — delegates to persistSvc.
func (s *Server) ExportContext(ctx context.Context, req *proto.ExportContextRequest) (*proto.ExportContextResponse, error) {
	return s.persistSvc.ExportContext(ctx, req)
}

// GetConfig implements proto.AgentServer — reports the current runtime config
// without exposing the literal API key. cloud_state is derived from the active
// cloud provider's Name() ("NONE" → "absent", everything else → "ok").
func (s *Server) GetConfig(ctx context.Context, req *proto.GetConfigRequest) (*proto.GetConfigResponse, error) {
	state := "absent"
	if r := s.providerSvc.Router(); r != nil {
		if cp, ok := r.GetModelProviders()["CloudModel"]; ok && cp != nil {
			if cp.Name() != "NONE" {
				state = "ok"
			}
		}
	}
	cfg := s.cfgSvc.Get()
	// Cloud fields fall back to the active profile when the legacy top-level
	// slots are empty — profiles are the single source of truth (see
	// activeCloudModel), and the legacy fields are only populated for
	// backwards compat. Without the fallback, GetConfig responds with an
	// empty CloudModel for any config that migrated to profile-only auth,
	// which drops "c:model" from the header.
	cloudModel := cfg.CloudModel
	if cloudModel == "" {
		cloudModel = s.activeCloudModel()
	}
	cloudProvider := cfg.CloudProvider
	cloudBaseURL := cfg.CloudBaseURL
	if p, ok := s.activeProfile(); ok {
		if cloudProvider == "" {
			cloudProvider = p.Flavor
		}
		if cloudBaseURL == "" {
			cloudBaseURL = p.BaseURL
		}
	}
	return &proto.GetConfigResponse{
		OllamaUrl:              cfg.OllamaURL,
		OpenModel:              cfg.OpenChatModel(),
		EmbeddingModel:         cfg.OpenEmbeddingModel(),
		CloudProvider:          cloudProvider,
		CloudModel:             cloudModel,
		CloudBaseUrl:           cloudBaseURL,
		CloudApiKeySet:         cfg.CloudAPIKey != "",
		CloudState:             state,
		Port:                   cfg.Port,
		OpenRuntime:            cfg.OpenRuntime,
		LocusMode:              cfg.LocusMode,
		WatchdogEnabled:        cfg.Watchdog.Enabled,
		WatchdogEcho:           cfg.Watchdog.Echo,
		WatchdogMode:           cfg.Watchdog.Mode,
		WatchdogChecks:         strings.Join(cfg.Watchdog.Checks, ","),
		WatchdogEscalateAfter:  strconv.Itoa(cfg.Watchdog.EscalateAfter),
		ElideToolResults:       cfg.Compaction.ElideToolResults,
		LossyToolElision:       cfg.Compaction.LossyToolElision,
		RawRetentionDays:       int32(cfg.Compaction.Retention.RawRetentionDays),
		CompactedRetentionDays: int32(cfg.Compaction.Retention.CompactedRetentionDays),
		KeepForever:            cfg.Compaction.Retention.KeepForever,
		CompactionEnabled:      cfg.Compaction.Enabled,
		ModelTiers:             cfg.Models.TierSlots(),
		ModelsDefaultProvider:  string(cfg.Models.DefaultProvider),
	}, nil
}

// ListModels implements proto.AgentServer — returns available models from the active local runtime.
func (s *Server) ListModels(ctx context.Context, req *proto.ListModelsRequest) (*proto.ListModelsResponse, error) {
	if s.providerSvc.Registry() == nil {
		return nil, fmt.Errorf("registry not configured")
	}
	runtimeName := s.cfgSvc.Get().OpenRuntime
	if runtimeName == "" {
		runtimeName = "ollama"
	}
	eng, err := s.providerSvc.Registry().GetEngine(runtimeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s engine: %v", runtimeName, err)
	}

	models, err := eng.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}

	protoModels := make([]*proto.ModelInfo, len(models))
	for i, m := range models {
		protoModels[i] = &proto.ModelInfo{
			Name:       m.Name,
			Size:       m.Size,
			ModifiedAt: m.ModifiedAt,
		}
	}

	return &proto.ListModelsResponse{Models: protoModels}, nil
}

// GetRuntimeStatus implements proto.AgentServer — returns the dashboard's
// provider-neutral runtime snapshot.
func (s *Server) GetRuntimeStatus(ctx context.Context, req *proto.GetRuntimeStatusRequest) (*proto.GetRuntimeStatusResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.GetRuntimeStatusResponse{}, nil
	}
	status, err := rm.Status(ctx)
	if err != nil {
		return nil, err
	}
	return &proto.GetRuntimeStatusResponse{
		Models:    mapRuntimeModels(status.Models),
		Instances: mapRuntimeInstances(status.Instances),
		Endpoints: mapRuntimeEndpoints(status.Endpoints),
		Logs:      mapRuntimeLogs(status.Logs),
	}, nil
}

// ListRuntimeModels implements proto.AgentServer.
//
// Returns three merged lists: (1) downloaded files on disk, (2) the
// hardcoded llama-server catalog, (3) the online Ollama library catalog
// (if the catalog manager is attached). Online catalog entries are
// deduped against the hardcoded catalog by family name — hardcoded
// wins because it has richer metadata (family, quantization) baked in.
//
// The response also carries catalog_updated_at (RFC3339) so the CLI
// dashboard can render "Catalog updated Nh ago" and color the label
// based on staleness.
func (s *Server) ListRuntimeModels(ctx context.Context, req *proto.ListRuntimeModelsRequest) (*proto.ListRuntimeModelsResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.ListRuntimeModelsResponse{}, nil
	}
	models, err := rm.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	resp := &proto.ListRuntimeModelsResponse{Models: mapRuntimeModels(models)}
	if cm := s.providerSvc.CatalogManager(); cm != nil {
		online := cm.Models()
		if len(online) > 0 {
			// Dedupe by family name against what we've already mapped.
			// Anything already present in models (hardcoded catalog OR
			// downloaded on disk) keeps its richer entry.
			seen := make(map[string]bool, len(resp.Models))
			for _, m := range resp.Models {
				if m.GetFamily() != "" {
					seen[m.GetFamily()] = true
				}
			}
			for _, m := range online {
				if seen[m.Name] {
					continue
				}
				resp.Models = append(resp.Models, onlineCatalogModelToProto(m))
			}
		}
		if fa := cm.FetchedAt(); !fa.IsZero() {
			resp.CatalogUpdatedAt = fa.UTC().Format(time.RFC3339)
		}
		// Enrich every ollama-backed entry with warmed estimate numbers
		// so the dashboard renders memory/fit lines with zero per-row
		// RPCs. Un-warmed entries keep zeros — the client falls back to
		// GetModelRAMEstimate on selection.
		for _, pm := range resp.Models {
			if pm.GetOllamaRef() == "" {
				continue
			}
			est, ok := cm.CachedEstimate(pm.GetOllamaRef())
			if !ok {
				continue
			}
			pm.KvBytesPerToken = est.KVBytesPerToken
			pm.MaxContextTokens = est.MaxContextTokens
			if pm.GetSizeBytes() == 0 {
				pm.SizeBytes = est.WeightsBytes
			}
		}
	}
	resp.SystemRamBytes = sysram.Total()
	return resp, nil
}

// onlineCatalogModelToProto converts one ollamacatalog.Model to the
// wire-level RuntimeModel shape. Sparse — the family list from
// ListModels doesn't include tags or sizes, so most fields are empty
// until the CLI drills into the family (which triggers a tag fetch on
// the next release). What we can fill in: display name (title-cased
// family), family, runtime (llama_server), source (catalog-online),
// format (gguf), download state (not_downloaded), and ollama_ref
// (which is what the download handler will use to route through the
// OCI blob flow).
func onlineCatalogModelToProto(m ollamacatalog.Model) *proto.RuntimeModel {
	return &proto.RuntimeModel{
		Id:            "llama_server:online:" + m.Name,
		DisplayName:   titleCase(m.Name),
		Runtime:       "llama_server",
		Source:        "catalog-online",
		Format:        "gguf",
		Family:        m.Name,
		DownloadState: "not_downloaded",
		OllamaRef:     m.Name, // colon+tag will be appended by CLI when user picks a size
		SupportsChat:  true,
	}
}

// titleCase renders "qwen2.5-coder" as "Qwen2.5 Coder" for display —
// cheap heuristic that gets Ollama's kebab-case names to something
// user-facing.
func titleCase(s string) string {
	if s == "" {
		return ""
	}
	// Split on hyphens; capitalize each token; join with a space.
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// RefreshOnlineCatalog implements proto.AgentServer — forces a fresh
// fetch of the online catalog, bypassing the 24h TTL. Used by the
// CLI dashboard's "R" refresh key.
func (s *Server) RefreshOnlineCatalog(ctx context.Context, req *proto.RefreshOnlineCatalogRequest) (*proto.RefreshOnlineCatalogResponse, error) {
	cm := s.providerSvc.CatalogManager()
	if cm == nil {
		return &proto.RefreshOnlineCatalogResponse{Error: "online catalog not configured"}, nil
	}
	if err := cm.Refresh(ctx); err != nil {
		// Refresh failure leaves the previous cache in place — surface
		// the error so the CLI can render it, but keep the timestamp
		// pointing at the most-recent SUCCESSFUL fetch so users can
		// still tell how stale the current view is.
		resp := &proto.RefreshOnlineCatalogResponse{Error: err.Error()}
		if fa := cm.FetchedAt(); !fa.IsZero() {
			resp.CatalogUpdatedAt = fa.UTC().Format(time.RFC3339)
		}
		return resp, nil
	}
	return &proto.RefreshOnlineCatalogResponse{
		CatalogUpdatedAt: cm.FetchedAt().UTC().Format(time.RFC3339),
		ModelCount:       int32(len(cm.Models())),
	}, nil
}

// ListRuntimeEndpoints implements proto.AgentServer.
func (s *Server) ListRuntimeEndpoints(ctx context.Context, req *proto.ListRuntimeEndpointsRequest) (*proto.ListRuntimeEndpointsResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.ListRuntimeEndpointsResponse{}, nil
	}
	endpoints, err := rm.Endpoints(ctx)
	if err != nil {
		return nil, err
	}
	return &proto.ListRuntimeEndpointsResponse{Endpoints: mapRuntimeEndpoints(endpoints)}, nil
}

// StartRuntimeModel implements proto.AgentServer.
func (s *Server) StartRuntimeModel(ctx context.Context, req *proto.StartRuntimeModelRequest) (*proto.StartRuntimeModelResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.StartRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	instance, err := rm.Start(ctx, localruntime.StartRequest{
		Runtime: req.GetRuntime(),
		ModelID: req.GetModelId(),
	})
	if err != nil {
		return &proto.StartRuntimeModelResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.StartRuntimeModelResponse{Ok: true, Instance: mapRuntimeInstance(*instance)}, nil
}

// StopRuntimeModel implements proto.AgentServer.
func (s *Server) StopRuntimeModel(ctx context.Context, req *proto.StopRuntimeModelRequest) (*proto.StopRuntimeModelResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.StopRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	if err := rm.Stop(ctx, localruntime.StopRequest{InstanceID: req.GetInstanceId()}); err != nil {
		return &proto.StopRuntimeModelResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.StopRuntimeModelResponse{Ok: true}, nil
}

// RestartRuntime implements proto.AgentServer.
func (s *Server) RestartRuntime(ctx context.Context, req *proto.RestartRuntimeRequest) (*proto.RestartRuntimeResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.RestartRuntimeResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	instance, err := rm.Restart(ctx, localruntime.RestartRequest{
		InstanceID: req.GetInstanceId(),
		Runtime:    req.GetRuntime(),
		ModelID:    req.GetModelId(),
	})
	if err != nil {
		return &proto.RestartRuntimeResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.RestartRuntimeResponse{Ok: true, Instance: mapRuntimeInstance(*instance)}, nil
}

// DownloadRuntimeModel implements proto.AgentServer.
//
// If the request carries an OllamaRef (e.g. "qwen2.5-coder:7b"), we
// treat this as an online-catalog entry that no provider has yet
// enumerated: enroll a fresh ModelRecord with the ref so the
// InMemoryManager's findDownloadModel finds it, then hand off to the
// existing DownloadModel path (which performs JIT OCI resolution).
// normalizeOllamaRef defaults a bare family name ("qwen2.5-coder") to
// the :latest tag. Online catalog entries carry tagless refs, but the
// registry needs a tag and the OCI resolver rejects tagless refs;
// Ollama's registry defines :latest for every library model. Empty
// stays empty (no online-catalog enrolment requested).
func normalizeOllamaRef(ref string) string {
	if ref == "" || strings.Contains(ref, ":") {
		return ref
	}
	return ref + ":latest"
}

// enrollmentRecord builds the inventory record for an ollama-backed
// download. Family and DisplayName MUST be set here: ListRuntimeModels
// dedupes online-catalog entries against the inventory by family name,
// so an enrolled record without one renders as a duplicate of the
// catalog entry it came from (and shows "family: (unknown)" in the
// detail panel) for its entire life.
func enrollmentRecord(modelID, runtime, ref string) localruntime.ModelRecord {
	family := ref
	if i := strings.Index(family, ":"); i > 0 {
		family = family[:i]
	}
	return localruntime.ModelRecord{
		ID:            modelID,
		Runtime:       runtime,
		OllamaRef:     ref,
		Family:        family,
		DisplayName:   titleCase(family),
		DownloadState: "not_downloaded",
		Format:        "gguf",
		SupportsChat:  true,
	}
}

func (s *Server) DownloadRuntimeModel(ctx context.Context, req *proto.DownloadRuntimeModelRequest) (*proto.DownloadRuntimeModelResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.DownloadRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	if ref := normalizeOllamaRef(req.GetOllamaRef()); ref != "" {
		// Only the concrete InMemoryManager supports enrolment. If a
		// future alternative implementation is wired in, this branch
		// is a no-op and the download will fall back to the provider
		// lookup below (which will fail cleanly with "not found").
		if imm, ok := rm.(*localruntime.InMemoryManager); ok {
			imm.EnrollDownload(enrollmentRecord(req.GetModelId(), req.GetRuntime(), ref))
		}
	}
	model, err := rm.DownloadModel(ctx, localruntime.DownloadRequest{
		Runtime: req.GetRuntime(),
		ModelID: req.GetModelId(),
	})
	if err != nil {
		return &proto.DownloadRuntimeModelResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.DownloadRuntimeModelResponse{Ok: true, Model: mapRuntimeModel(*model)}, nil
}

// CancelRuntimeModelDownload implements proto.AgentServer.
func (s *Server) CancelRuntimeModelDownload(ctx context.Context, req *proto.CancelRuntimeModelDownloadRequest) (*proto.CancelRuntimeModelDownloadResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.CancelRuntimeModelDownloadResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	model, err := rm.CancelDownload(ctx, localruntime.DownloadRequest{
		Runtime: req.GetRuntime(),
		ModelID: req.GetModelId(),
	})
	if err != nil {
		return &proto.CancelRuntimeModelDownloadResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.CancelRuntimeModelDownloadResponse{Ok: true, Model: mapRuntimeModel(*model)}, nil
}

// DeleteRuntimeModel implements proto.AgentServer.
func (s *Server) DeleteRuntimeModel(ctx context.Context, req *proto.DeleteRuntimeModelRequest) (*proto.DeleteRuntimeModelResponse, error) {
	rm := s.runtimeMgr()
	if rm == nil {
		return &proto.DeleteRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	if err := rm.DeleteModel(ctx, localruntime.DeleteModelRequest{
		Runtime: req.GetRuntime(),
		ModelID: req.GetModelId(),
	}); err != nil {
		return &proto.DeleteRuntimeModelResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.DeleteRuntimeModelResponse{Ok: true}, nil
}

// StreamRuntimeLogs implements proto.AgentServer. The first version streams the
// current server-side buffer; live follow can build on the same RPC later.
func (s *Server) StreamRuntimeLogs(req *proto.StreamRuntimeLogsRequest, stream proto.Agent_StreamRuntimeLogsServer) error {
	rm := s.runtimeMgr()
	if rm == nil {
		return nil
	}
	logs, err := rm.Logs(stream.Context(), localruntime.LogRequest{
		Tail:   int(req.GetTail()),
		Source: req.GetSource(),
	})
	if err != nil {
		return err
	}
	for _, entry := range logs {
		if err := stream.Send(mapRuntimeLog(entry)); err != nil {
			return err
		}
	}
	return nil
}

// refreshRuntimeEndpoints snapshots the current config via cfgSvc and pushes
// the derived endpoints. UpdateConfig calls applyRuntimeEndpoints directly
// with its already-mutated local snapshot instead.
func (s *Server) refreshRuntimeEndpoints() {
	if s.runtimesSvc != nil {
		s.runtimesSvc.RefreshRuntimeEndpoints()
	}
}

// applyRuntimeEndpoints derives and pushes runtime endpoints from the given
// config snapshot. Lock-free: the caller owns lock discipline.
func (s *Server) applyRuntimeEndpoints(cfg config.Config) {
	if s.runtimesSvc != nil {
		s.runtimesSvc.ApplyRuntimeEndpoints(cfg)
	}
}

func mapRuntimeModels(models []localruntime.ModelRecord) []*proto.RuntimeModel {
	out := make([]*proto.RuntimeModel, 0, len(models))
	for _, model := range models {
		out = append(out, mapRuntimeModel(model))
	}
	return out
}

func mapRuntimeModel(model localruntime.ModelRecord) *proto.RuntimeModel {
	return &proto.RuntimeModel{
		Id:                 model.ID,
		DisplayName:        model.DisplayName,
		Runtime:            model.Runtime,
		Source:             model.Source,
		Path:               model.Path,
		Format:             model.Format,
		Family:             model.Family,
		Quantization:       model.Quantization,
		SizeBytes:          model.SizeBytes,
		ModifiedAt:         formatRuntimeTime(model.ModifiedAt),
		DownloadState:      model.DownloadState,
		DownloadUrl:        model.DownloadURL,
		DownloadedBytes:    model.DownloadedBytes,
		DownloadTotalBytes: model.DownloadTotalBytes,
		DownloadError:      model.DownloadError,
		RuntimeState:       model.RuntimeState,
		SupportsChat:       model.SupportsChat,
		SupportsEmbed:      model.SupportsEmbed,
		SupportsTools:      model.SupportsTools,
		Active:             model.Active,
	}
}

func mapRuntimeInstances(instances []localruntime.InstanceRecord) []*proto.RuntimeInstance {
	out := make([]*proto.RuntimeInstance, 0, len(instances))
	for _, instance := range instances {
		out = append(out, mapRuntimeInstance(instance))
	}
	return out
}

func mapRuntimeInstance(instance localruntime.InstanceRecord) *proto.RuntimeInstance {
	return &proto.RuntimeInstance{
		Id:           instance.ID,
		Runtime:      instance.Runtime,
		ModelId:      instance.ModelID,
		State:        instance.State,
		Pid:          int32(instance.PID),
		Address:      instance.Address,
		Port:         int32(instance.Port),
		Endpoint:     instance.Endpoint,
		StartedAt:    formatRuntimeTime(instance.StartedAt),
		ReadyAt:      formatRuntimeTime(instance.ReadyAt),
		RestartCount: int32(instance.RestartCount),
		LastExitCode: int32(instance.LastExitCode),
		LastError:    instance.LastError,
		LogPath:      instance.LogPath,
	}
}

func mapRuntimeEndpoints(endpoints []localruntime.EndpointRecord) []*proto.RuntimeEndpoint {
	out := make([]*proto.RuntimeEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		out = append(out, &proto.RuntimeEndpoint{
			Id:            endpoint.ID,
			Kind:          endpoint.Kind,
			DisplayName:   endpoint.DisplayName,
			BaseUrl:       endpoint.BaseURL,
			Scope:         endpoint.Scope,
			State:         endpoint.State,
			ActiveRoles:   append([]string(nil), endpoint.ActiveRoles...),
			Models:        append([]string(nil), endpoint.Models...),
			LastCheckedAt: formatRuntimeTime(endpoint.LastCheckedAt),
			LatencyMs:     endpoint.LatencyMS,
			LastError:     endpoint.LastError,
			AuthState:     endpoint.AuthState,
		})
	}
	return out
}

func mapRuntimeLogs(logs []localruntime.LogEntry) []*proto.RuntimeLogEntry {
	out := make([]*proto.RuntimeLogEntry, 0, len(logs))
	for _, entry := range logs {
		out = append(out, mapRuntimeLog(entry))
	}
	return out
}

func mapRuntimeLog(entry localruntime.LogEntry) *proto.RuntimeLogEntry {
	return &proto.RuntimeLogEntry{
		Timestamp: formatRuntimeTime(entry.Timestamp),
		Source:    entry.Source,
		Level:     entry.Level,
		RuntimeId: entry.RuntimeID,
		ModelId:   entry.ModelID,
		Message:   entry.Message,
	}
}

func formatRuntimeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ProcessRequest implements proto.AgentServer (Unary).
func (s *Server) ProcessRequest(ctx context.Context, req *proto.ProcessRequestRequest) (*proto.ProcessRequestResponse, error) {
	fmt.Printf("Received request (Unary): %s\n", req.Input)

	agentReq := s.mapRequest(req)
	response, err := s.agent.ProcessRequest(ctx, agentReq)
	if err != nil {
		fmt.Printf("ProcessRequest error: %v\n", err)
		return nil, fmt.Errorf("agent error: %w", err)
	}

	fmt.Printf("ProcessRequest completed successfully\n")
	return s.mapResponse(response), nil
}

// StreamProcessRequest implements proto.AgentServer (Streaming).
func (s *Server) StreamProcessRequest(req *proto.ProcessRequestRequest, stream proto.Agent_StreamProcessRequestServer) error {
	fmt.Printf("Received request (Stream): %s\n", req.Input)

	if (s.providerSvc.Cloud() != nil || s.providerSvc.Open() != nil) && s.toolSvc.Registry() != nil {
		return s.streamProcessRequestWithToolLoop(req, stream)
	}

	agentReq := s.mapRequest(req)
	// Announce the routed engine so the client can show a live engine badge.
	agentReq.OnRoute = func(model string, isCloud bool) {
		stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_RouteSelected{
				RouteSelected: &proto.RouteSelected{Model: model, IsCloud: isCloud},
			},
		})
	}

	response, err := s.agent.ProcessRequestStream(stream.Context(), agentReq,
		func(msg string) {
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_Progress{
					Progress: &proto.ProgressUpdate{Message: msg},
				},
			})
		},
		func(token string) {
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_TokenDelta{
					TokenDelta: &proto.TokenDelta{Content: token},
				},
			})
		},
	)

	if err != nil {
		return fmt.Errorf("agent error: %w", err)
	}

	// Send final response
	return stream.Send(&proto.StreamProcessResponse{
		Payload: &proto.StreamProcessResponse_FinalResponse{
			FinalResponse: s.mapResponse(response),
		},
	})
}

// loopEnv is the environment grounding rendered into the tool-loop system
// prompt's <env> block.
type loopEnv struct {
	WorkDir   string
	Platform  string
	Date      string
	GitRepo   bool
	GitBranch string
}

// buildToolLoopSystem assembles the tool-loop system prompt: an <env> block
// (cwd, platform, date, git), a <directory> snapshot of the cwd's immediate
// children, and the project's .cercano/context.md when present. The cwd + the
// snapshot are what stop the cloud model from hunting the filesystem to locate
// the project before it can do any real work.
func buildToolLoopSystem(env loopEnv, steering, dirSnapshot, projectContext string) string {
	var b strings.Builder
	b.WriteString("You are Cercano, an agentic coding assistant operating in a terminal.\n\n")
	b.WriteString("A note on tool naming: depending on your cloud route, some tools in your schema may appear under a host prefix like `mcp__oc__Read` instead of plain `Read`. That prefix is a wire-level routing artifact from the provider (e.g. an OpenCode/Meridian adapter) — it does not mean you are running inside a different host. You are Cercano either way. Call tools using whatever name is in your schema. But when you pass tool names as data — for example, in the `tools` argument of `dispatch` or `workflow` — always use the plain registered names (Read, Write, Edit, Bash, Glob, Grep, LS, git_status, etc.) without any host prefix.\n\n")
	b.WriteString("Never end your turn on a promise. Your turn ends the moment you send a reply with no tool calls — anything you say you are \"about to\" do (\"let me check…\", \"running it now…\") will never happen unless you do it in this same turn, with tool calls, before replying. Either do the work now, or state plainly that you are not doing it and why. Never claim you checked, ran, or verified something unless a tool call in this turn actually did it.\n\n")
	if strings.TrimSpace(steering) != "" {
		b.WriteString(steering)
		b.WriteString("\n\n")
	}
	b.WriteString("<env>\n")
	if env.WorkDir != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", env.WorkDir)
	}
	if env.Platform != "" {
		fmt.Fprintf(&b, "Platform: %s\n", env.Platform)
	}
	if env.Date != "" {
		fmt.Fprintf(&b, "Today's date: %s\n", env.Date)
	}
	if env.GitRepo {
		if env.GitBranch != "" {
			fmt.Fprintf(&b, "Is a git repository: yes (branch %s)\n", env.GitBranch)
		} else {
			b.WriteString("Is a git repository: yes\n")
		}
	} else {
		b.WriteString("Is a git repository: no\n")
	}
	b.WriteString("</env>\n\n")
	b.WriteString("Resolve relative file paths against the working directory; don't search the filesystem to locate the project.\n")
	if strings.TrimSpace(dirSnapshot) != "" {
		b.WriteString("\n<directory>\n")
		b.WriteString(strings.TrimRight(dirSnapshot, "\n"))
		b.WriteString("\n</directory>\n")
	}
	if strings.TrimSpace(projectContext) != "" {
		b.WriteString("\n<project-context>\n")
		b.WriteString(strings.TrimRight(projectContext, "\n"))
		b.WriteString("\n</project-context>\n")
	}
	return b.String()
}

// buildSystemPrompt gathers live environment grounding for workDir and renders
// the tool-loop system prompt.
func (s *Server) buildSystemPrompt(workDir string) string {
	env := loopEnv{
		WorkDir:  workDir,
		Platform: runtime.GOOS,
		Date:     time.Now().Format("2006-01-02"),
	}
	if workDir != "" {
		env.GitRepo, env.GitBranch = gitInfo(workDir)
	}
	steering := protocols.SteeringBlock(protocols.ForDomain(protocols.DomainCore))
	projectCtx := ""
	if s.persistSvc != nil {
		projectCtx = s.persistSvc.LoadProjectContext(workDir)
	}
	return buildToolLoopSystem(env, steering, directorySnapshot(workDir, 80), projectCtx)
}

// directorySnapshot lists the immediate children of dir — directories first
// (trailing slash), then files — skipping dot-entries, capped at max with a
// "(N more)" note so the prompt stays bounded.
func directorySnapshot(dir string, max int) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var dirs, files []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name+"/")
		} else {
			files = append(files, name)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	all := append(dirs, files...)
	more := 0
	if len(all) > max {
		more = len(all) - max
		all = all[:max]
	}
	out := strings.Join(all, "\n")
	if more > 0 {
		out += fmt.Sprintf("\n… (%d more)", more)
	}
	return out
}

// gitInfo reports whether dir is inside a git work tree and its current branch.
func gitInfo(dir string) (bool, string) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return false, ""
	}
	return true, strings.TrimSpace(string(out))
}

// AttachConversation implements proto.AgentServer: a second surface subscribes
// (read-only) to a conversation's turn events without starting a new turn.
//
// Behaviour:
//   - Attach returns a defensive-copy replay buffer (events already published
//     in the current or most-recent turn) plus a live channel.
//   - The replay is sent first, in order, via sendRunnerEvent.
//   - Then live events are forwarded until (a) the broker closes ch (the turn
//     ended / superseded and its generation retired), or (b) the client
//     disconnects (stream.Context().Done()).
//   - Lossy attach: if this observer drains slowly, events are dropped rather
//     than stalling the turn's publisher. A passive observer must not block
//     the initiator.
//   - Observe-only: no permission prompting, no persistence, no turn gating.
func (s *Server) AttachConversation(req *proto.AttachConversationRequest, stream proto.Agent_AttachConversationServer) error {
	convID := req.GetConversationId()
	if convID == "" {
		return grpcstatus.Error(codes.InvalidArgument, "conversation_id is required")
	}

	replay, ch, detach := s.turnBroker.Attach(convID)
	defer detach()

	// Send the replay buffer first (events published before this Attach call).
	for _, ev := range replay {
		if err := sendRunnerEvent(stream, ev); err != nil {
			return err
		}
	}

	// Forward live events until the channel closes or the client disconnects.
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				// Broker closed ch — turn ended or was superseded.
				return nil
			}
			if err := sendRunnerEvent(stream, ev); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// streamProcessRequestWithToolLoop drives the native tool-calling loop and
// emits per-event stream payloads. Used when a layered LLM provider has been
// wired via SetCloudLLMProvider.
//
// This is the host-side "front door": it handles the per-conversation turn
// fence (BeginTurn), attaches the initiator as the first subscriber on the
// conversation broker, runs the turn in a goroutine that publishes events to
// the broker, and drains those events to the initiator's stream. This is the
// unified model: the initiator is just the first (and, until Task 4, only)
// subscriber — identical to what an attacher will do in Task 4.
//
// Single-surface behavior is byte-identical to the old hostProtoSink path:
// the same events are mapped to the same proto payloads, in the same order,
// by sendRunnerEvent — the only difference is the indirection through the
// broker's channel rather than a direct stream.Send in Emit.
func (s *Server) streamProcessRequestWithToolLoop(req *proto.ProcessRequestRequest, stream proto.Agent_StreamProcessRequestServer) error {
	// One live turn per conversation. A new turn here supersedes any turn still
	// running on the same conversation (cancels its ctx); this turn's own ctx
	// is canceled if a later turn supersedes IT. turnGen fences persistence so
	// a superseded turn's late writes never interleave into the live history.
	ctx, turnGen, releaseTurn := s.beginTurn(stream.Context(), req.GetConversationId())
	defer releaseTurn()

	convID := req.GetConversationId()

	// Attach the initiator as the first subscriber using a LOSSLESS subscription.
	// replay is empty at this point — no events have been published for this turn
	// yet (BeginTurn just reset the buffer). detach closes ch and removes us from
	// the fan-out set.
	//
	// The initiator uses AttachLossless (not Attach) because its stream is the
	// turn's authoritative output: every event must arrive, even if stream.Send
	// is momentarily slow. Passive Task-4 attachers use Attach (drop-on-full).
	replay, ch, detach := s.turnBroker.AttachLossless(convID)
	defer detach()

	// requester gates a W/X permission prompt: blocks until the client responds
	// via AllowToolCall / DenyToolCall RPCs. Unchanged from the old closure.
	requester := runnersvc.PermissionRequester(func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		if s.permBroker == nil || !s.permBroker.HasPending() {
			return false, nil
		}
		if err := stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_PermissionRequired{
				PermissionRequired: &proto.PermissionRequired{
					ToolUseId:   toolUseID,
					ToolName:    name,
					ArgsJson:    string(args),
					Tier:        string(tier),
					Destructive: destructive,
				},
			},
		}); err != nil {
			return false, err
		}
		d, err := s.permBroker.Wait(ctx, toolUseID)
		if err != nil {
			return false, err
		}
		if d.Allow && d.Persist {
			if tool, ok := s.toolSvc.Registry().Get(name); ok && agenttools.OriginOf(tool) == agenttools.OriginMCP {
				if err := s.permBroker.AddMCPAllow(name); err != nil {
					fmt.Fprintf(os.Stderr, "[mcp] persist always-allow for %s: %v\n", name, err)
				}
			}
		}
		return d.Allow, nil
	})

	// persist is the host-fenced turn persister. The generation fence ensures a
	// superseded turn's late writes cannot land in the live history.
	var persist runnersvc.PersistFunc
	if convID != "" {
		persist = func(m llm.Message) {
			if !s.turnIsCurrent(convID, turnGen) {
				return
			}
			s.persistTurn(ctx, convID, m)
		}
	}

	// brokerSink publishes runner events to the turn broker so all subscribers
	// (including this initiator) receive them via Attach channels.
	sink := &brokerSink{broker: s.turnBroker, conv: convID, gen: turnGen}

	runReq := runnersvc.Request{
		ConversationID: req.GetConversationId(),
		Input:          req.GetInput(),
		Images:         mapInlineImages(req.GetImages()),
		WorkDir:        req.GetWorkDir(),
		Gen:            turnGen,
	}

	// turnResult carries RunTurn's return values from the goroutine to the
	// main goroutine. Written exactly once before doneCh receives; read after.
	type turnResult struct {
		result runnersvc.Result
		err    error
	}
	doneCh := make(chan turnResult, 1)

	// Run the turn concurrently so the main goroutine can drain the broker
	// channel without blocking RunTurn (which calls sink.Emit synchronously).
	//
	// Panic recovery: a panic inside RunTurn would otherwise crash the whole
	// process (kills every client). We recover it here, log the stack (matching
	// RecoveryStreamInterceptor's style in recovery.go), and write a
	// codes.Internal result to doneCh so the main goroutine can return cleanly.
	// doneCh is written exactly once: either the normal return at the end of the
	// func, or the recover path — never both (the recover fires only when a panic
	// unwinds past the normal write).
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic recovered in StreamProcessRequest: %v\n%s", r, debug.Stack())
				doneCh <- turnResult{err: grpcstatus.Errorf(codes.Internal, "internal server error")}
			}
		}()
		// Per-turn runner pick. See pickTurnRunner: in-process by default; the
		// worker only when armed AND this turn touches no host-side MCP tool.
		res, err := s.pickTurnRunner().RunTurn(ctx, runReq, sink, requester, persist)
		doneCh <- turnResult{result: res, err: err}
	}()

	// Drain the initiator's subscriber channel to the stream.
	//
	// Order: send replay events first (guaranteed empty here — BeginTurn just
	// reset the buffer and no Publish has happened yet), then live events from
	// ch. When doneCh fires the turn is complete; drain any remaining buffered
	// events in ch before proceeding to send FinalResponse.
	for _, ev := range replay {
		if err := sendRunnerEvent(stream, ev); err != nil {
			// Stream gone; goroutine will notice ctx cancellation and return.
			<-doneCh
			return err
		}
	}

	var tr turnResult
	draining := false
	for !draining {
		select {
		case ev, ok := <-ch:
			if !ok {
				// ch closed by detach (deferred) — should not happen here because
				// detach runs after this loop, but handle defensively.
				draining = true
			} else {
				if err := sendRunnerEvent(stream, ev); err != nil {
					<-doneCh
					return err
				}
			}
		case tr = <-doneCh:
			draining = true
		}
	}

	// Drain any events still buffered in ch after the turn completed. This
	// ensures trailing events (e.g. a final tool-exec-complete published just
	// before RunTurn returned) are not dropped.
drainLoop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drainLoop
			}
			if err := sendRunnerEvent(stream, ev); err != nil {
				return err
			}
		default:
			break drainLoop
		}
	}

	if tr.err != nil {
		return tr.err
	}

	// If the runner returned a synthetic locus-error FinalText (no real loop
	// ran), wrap it in a FinalResponse and exit.  The runner signals this by
	// returning a non-empty FinalText with no Model set.
	if tr.result.Model == "" {
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_FinalResponse{
				FinalResponse: &proto.ProcessRequestResponse{Output: tr.result.FinalText},
			},
		})
	}

	// Send the final response. The runner has already done post-turn bookkeeping
	// (ScheduleRecap, RecordContextUsage) before returning.
	return stream.Send(&proto.StreamProcessResponse{
		Payload: &proto.StreamProcessResponse_FinalResponse{
			FinalResponse: &proto.ProcessRequestResponse{
				Output: strings.ToValidUTF8(tr.result.FinalText, "�"),
				RoutingMetadata: &proto.RoutingMetadata{
					ModelName: tr.result.Model,
				},
				// Carry token counts so the CLI's "last turn" footer isn't stuck at 0.
				InputTokens:  int32(tr.result.InputTokens),
				OutputTokens: int32(tr.result.OutputTokens),
			},
		},
	})
}

// brokerSink implements runner.EventSink by publishing each event to the turn
// broker, fenced by the conversation's current generation. All subscribers
// (including the turn initiator, attached via Attach) receive the event on
// their channels.
type brokerSink struct {
	broker *broker.Broker
	conv   string
	gen    uint64
}

func (s *brokerSink) Emit(ev runnersvc.Event) {
	s.broker.Publish(s.conv, s.gen, ev)
}

// streamResponseSender is the minimal interface required by sendRunnerEvent so
// both proto.Agent_StreamProcessRequestServer and
// proto.Agent_AttachConversationServer (which are both type aliases for
// grpc.ServerStreamingServer[proto.StreamProcessResponse]) can be passed
// without duplicating the mapping switch.
type streamResponseSender interface {
	Send(*proto.StreamProcessResponse) error
}

// sendRunnerEvent maps a runner.Event to the appropriate proto.StreamProcessResponse
// payload and sends it on stream. This is the extracted mapping switch formerly
// inlined in hostProtoSink.Emit. It is a free function so the Task-4
// AttachConversation handler can reuse the same mapping without duplicating it.
//
// FinalResponse is NOT sent here (the handler sends it after RunTurn returns).
// EventWatchdog/escalate is silently dropped — behavior-preserving (old sink
// did the same).
func sendRunnerEvent(stream streamResponseSender, ev runnersvc.Event) error {
	switch ev.Kind {
	case runnersvc.EventProgress:
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_Progress{
				Progress: &proto.ProgressUpdate{Message: ev.Text},
			},
		})

	case runnersvc.EventRouteSelected:
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_RouteSelected{
				RouteSelected: &proto.RouteSelected{Model: ev.Model, IsCloud: ev.IsCloud},
			},
		})

	case runnersvc.EventToken:
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_TokenDelta{
				TokenDelta: &proto.TokenDelta{Content: ev.Text},
			},
		})

	case runnersvc.EventToolUseStart:
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_ToolUseStart{
				ToolUseStart: &proto.ToolUseStart{
					ToolUseId: ev.ToolUseID,
					ToolName:  ev.ToolName,
				},
			},
		})

	case runnersvc.EventToolUseStop:
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_ToolUseStop{
				ToolUseStop: &proto.ToolUseStop{
					ToolUseId:   ev.ToolUseID,
					ArgsSummary: ev.ArgsSummary,
				},
			},
		})

	case runnersvc.EventToolExecStart:
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_ToolExecStart{
				ToolExecStart: &proto.ToolExecStart{
					ToolUseId: ev.ToolUseID,
				},
			},
		})

	case runnersvc.EventToolExecComplete:
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_ToolExecComplete{
				ToolExecComplete: &proto.ToolExecComplete{
					ToolUseId: ev.ToolUseID,
					Summary:   ev.Summary,
					Detail:    ev.Detail,
					StartLine: int32(ev.StartLine),
					IsError:   ev.IsError,
				},
			},
		})

	case runnersvc.EventWatchdog:
		switch ev.WatchdogKind {
		case "challenge", "block":
			return stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_WatchdogEvent{
					WatchdogEvent: &proto.WatchdogEvent{
						Kind: ev.WatchdogKind, Protocol: ev.Detail, Text: ev.Summary,
					},
				},
			})
		case "echo":
			return stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_WatchdogEvent{
					WatchdogEvent: &proto.WatchdogEvent{
						Kind: "echo", Text: ev.Summary, Thread: ev.Thread,
					},
				},
			})
		case "escalate":
			// Behavior-preserving: old sink dropped LoopWatchdogEscalate (no send).
		}

	case runnersvc.EventDone:
		// Not used by the in-process host (result comes back from RunTurn directly).
	}
	return nil
}

// primaryModel returns the model the context meter measures against: the
// locus route's primary serving model. Delegates to providerSvc.PrimaryModel().
func (s *Server) primaryModel() string {
	return s.providerSvc.PrimaryModel()
}

// mainModelFor returns the configured model name for the active tier.
// Delegates to providerSvc.MainModel().
func (s *Server) mainModelFor(isCloud bool) string {
	return s.providerSvc.MainModel(isCloud)
}

// persistTurn is a front-door shim used by the tool loop and tests.
// The canonical implementation lives in hostsvc/persistence.
func (s *Server) persistTurn(ctx context.Context, convID string, m llm.Message) {
	s.persistSvc.PersistTurn(ctx, convID, m)
}

// assembleHistory is a front-door shim for the tool loop and test suite.
// The canonical implementation lives in hostsvc/persistence (no store param).
// The store parameter is accepted but ignored — the service owns the store.
func (s *Server) assembleHistory(ctx context.Context, _ conversation.Store, convID string) []llm.Message {
	return s.persistSvc.AssembleHistory(ctx, convID)
}

// mapInlineImages converts proto images to agent.InlineImage.
func mapInlineImages(in []*proto.InlineImage) []agent.InlineImage {
	if len(in) == 0 {
		return nil
	}
	out := make([]agent.InlineImage, 0, len(in))
	for _, p := range in {
		out = append(out, agent.InlineImage{
			Index:     int(p.GetIndex()),
			Data:      p.GetData(),
			MediaType: p.GetMediaType(),
		})
	}
	return out
}

func (s *Server) mapRequest(req *proto.ProcessRequestRequest) *agent.Request {
	return &agent.Request{
		Input:          req.Input,
		WorkDir:        req.WorkDir,
		FileName:       req.FileName,
		ConversationID: req.ConversationId,
		DirectOpen:     req.DirectOpen,
		ModelOverride:  req.ModelOverride,
		Coproc:         req.Coproc,
		Images:         mapInlineImages(req.GetImages()),
	}
}

// SetPermissionMode implements proto.AgentServer.
func (s *Server) SetPermissionMode(ctx context.Context, req *proto.SetPermissionModeRequest) (*proto.SetPermissionModeResponse, error) {
	if s.permBroker == nil {
		return &proto.SetPermissionModeResponse{Ok: false, Error: "permission store not configured"}, nil
	}
	m, err := agent.ParseMode(req.GetMode())
	if err != nil {
		return &proto.SetPermissionModeResponse{Ok: false, Error: err.Error()}, nil
	}
	if err := s.permBroker.SetMode(m); err != nil {
		return &proto.SetPermissionModeResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.SetPermissionModeResponse{Ok: true}, nil
}

// GetPermissionMode implements proto.AgentServer.
func (s *Server) GetPermissionMode(ctx context.Context, req *proto.GetPermissionModeRequest) (*proto.GetPermissionModeResponse, error) {
	if s.permBroker == nil {
		return &proto.GetPermissionModeResponse{Mode: string(agent.ModePermissive)}, nil
	}
	return &proto.GetPermissionModeResponse{Mode: string(s.permBroker.Mode())}, nil
}

// AllowToolCall implements proto.AgentServer.
func (s *Server) AllowToolCall(ctx context.Context, req *proto.AllowToolCallRequest) (*proto.AllowToolCallResponse, error) {
	if s.permBroker == nil {
		return &proto.AllowToolCallResponse{Ok: false}, nil
	}
	ok := s.permBroker.Resolve(req.GetToolUseId(), agent.Decision{Allow: true, Persist: req.GetPersist()})
	return &proto.AllowToolCallResponse{Ok: ok}, nil
}

// DenyToolCall implements proto.AgentServer.
func (s *Server) DenyToolCall(ctx context.Context, req *proto.DenyToolCallRequest) (*proto.DenyToolCallResponse, error) {
	if s.permBroker == nil {
		return &proto.DenyToolCallResponse{Ok: false}, nil
	}
	ok := s.permBroker.Resolve(req.GetToolUseId(), agent.Decision{Allow: false})
	return &proto.DenyToolCallResponse{Ok: ok}, nil
}

// GetProviderCapabilities implements proto.AgentServer.
func (s *Server) GetProviderCapabilities(ctx context.Context, req *proto.GetProviderCapabilitiesRequest) (*proto.GetProviderCapabilitiesResponse, error) {
	if s.providerSvc.Cloud() == nil {
		return &proto.GetProviderCapabilitiesResponse{
			SupportsTools:         true,
			SupportsParallelTools: true,
			SupportsCaching:       true,
			SupportsVision:        true,
			MaxToolsPerCall:       0,
		}, nil
	}
	c := s.providerSvc.Cloud().Capabilities()
	return &proto.GetProviderCapabilitiesResponse{
		SupportsTools:         c.SupportsTools,
		SupportsParallelTools: c.SupportsParallelTools,
		SupportsCaching:       c.SupportsCaching,
		SupportsVision:        c.SupportsVision,
		MaxToolsPerCall:       int32(c.MaxToolsPerCall),
	}, nil
}

// MapResponseForTest exposes mapResponse for testing.
func (s *Server) MapResponseForTest(response *agent.Response) *proto.ProcessRequestResponse {
	return s.mapResponse(response)
}

func (s *Server) mapResponse(response *agent.Response) *proto.ProcessRequestResponse {
	// Sanitize output to valid UTF-8 — gRPC requires all string fields
	// to be valid UTF-8 and will fail marshaling otherwise.
	protoRes := &proto.ProcessRequestResponse{
		Output: strings.ToValidUTF8(response.Output, "\uFFFD"),
		Notice: response.Notice,
	}

	if len(response.FileChanges) > 0 {
		protoRes.FileChanges = make([]*proto.FileChange, len(response.FileChanges))
		for i, fc := range response.FileChanges {
			action := proto.FileAction_UPDATE
			switch fc.Action {
			case "CREATE":
				action = proto.FileAction_CREATE
			case "DELETE":
				action = proto.FileAction_DELETE
			}
			protoRes.FileChanges[i] = &proto.FileChange{
				Path:    fc.Path,
				Content: fc.Content,
				Action:  action,
			}
		}
	}

	rm := &proto.RoutingMetadata{
		ModelName:  response.RoutingMetadata.ModelName,
		Confidence: float32(response.RoutingMetadata.Confidence),
		Escalated:  response.RoutingMetadata.Escalated,
		IsCloud:    response.RoutingMetadata.IsCloud,
	}

	if r := s.providerSvc.Registry(); r != nil {
		if eng, err := r.GetEngine("ollama"); err == nil {
			if confEng, ok := eng.(engine.ConfigurableEngine); ok {
				rm.Endpoint = confEng.GetActiveURL()
				rm.IsFallback = confEng.IsUsingFallback()
			}
		}
	}

	protoRes.RoutingMetadata = rm

	protoRes.ValidationErrors = response.ValidationErrors
	protoRes.InputTokens = int32(response.InputTokens)
	protoRes.OutputTokens = int32(response.OutputTokens)

	return protoRes
}
