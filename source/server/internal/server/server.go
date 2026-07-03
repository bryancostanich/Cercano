package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactiongen"
	"cercano/source/server/internal/compactor"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/anthropic"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/loop"
	mcphost "cercano/source/server/internal/mcp_host"
	"cercano/source/server/internal/meridian"
	"cercano/source/server/internal/protocols"
	"cercano/source/server/internal/retention"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/watchdog"
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
	agent               *agent.Agent
	openProvider       *legacymodels.OpenModelProvider
	router              RouterCloudUpdater
	coordinator         *loop.ADKCoordinator
	cloudFactory        agent.CloudFactory
	registry            *engine.EngineRegistry
	healthMonitorCancel context.CancelFunc // cancel function for the active health monitor
	configPath          string             // path to config.yaml for persistence
	currentConfig       config.Config      // current config state for persistence
	toolRegistry        *agenttools.Registry
	capRegistry         *capabilities.Registry
	permStore           *agent.PermissionStore
	mcpManager          McpManager
	meridianMgr         *meridian.Manager
	pendingDecisions    *agent.PendingDecisions
	cloudLLMProvider    llm.Provider
	openLLMProvider    llm.Provider // native-tool-loop local provider (Ollama)
	secrets             secrets.Store
	runtimeManager      localruntime.Manager
	// catalogManager (optional) surfaces Ollama's public library as an
	// online model catalog. Nil = no online catalog (dashboard just
	// shows the hardcoded local catalog + any downloaded files).
	catalogManager *ollamacatalog.Manager
	retentionSweeper    *retention.Sweeper
	compactionGen       *compactiongen.Generator
	contextLoader       *projectctx.Loader
	dispatchEngine      *dispatch.Engine
	watchdog            *watchdog.Watchdog // protocol-supervision gate; nil = disabled (default)
	usageSink           func(usage.Usage)  // wraps the main-loop provider for token recording

	events        *eventHub    // server->client push fan-out (SubscribeEvents)
	permBcastMu   sync.Mutex   // guards lastBcastMode
	lastBcastMode string       // last permission mode broadcast; dedupes file-watcher vs SetMode
	cfgMu         sync.RWMutex // guards all access to currentConfig
}

// SetContextLoader wires the project-context loader so the native tool-loop can
// include .cercano/context.md (and the working directory) in its system prompt.
func (s *Server) SetContextLoader(l *projectctx.Loader) { s.contextLoader = l }

// SetDispatchEngine wires the unified dispatch engine so capability Services
// can dispatch one-shot co-processor work, and installs the agentic runner
// so Agentic dispatches can call agent.RunToolLoop without creating an import
// cycle between internal/dispatch and internal/agent.
// Call before InstallCapabilities.
func (s *Server) SetDispatchEngine(e *dispatch.Engine) {
	s.dispatchEngine = e
	if e != nil {
		e.SetAgenticRunner(s.runAgenticDispatch)
	}
}

// SetToolRegistry attaches the agent's tool registry. The CLI's /tools and
// /tool commands route through ListTools / InvokeTool RPCs to it.
func (s *Server) SetToolRegistry(r *agenttools.Registry) { s.toolRegistry = r }

// ToolRegistry returns the current agent tool registry. Used by the MCP host
// to register dynamically connected tools into the same registry.
func (s *Server) ToolRegistry() *agenttools.Registry { return s.toolRegistry }

// InstallCapabilities builds the capability registry from the server's current
// providers, config, and context loader, then wires the resulting
// agenttools.Registry as the server's tool registry. Call AFTER
// SetCloudLLMProvider / SetOpenLLMProvider / SetContextLoader so that
// Services carries live runtime values.
func (s *Server) InstallCapabilities() {
	capReg := capabilities.NewRegistry(capabilities.Services{
		CloudProvider: s.cloudLLMProvider,
		OpenProvider: s.openLLMProvider,
		Config:        &s.currentConfig,
		ProjectCtx:    s.contextLoader,
		// Engine/Conversations wired in a later phase; nil-safe until then.
		Dispatch: func(ctx context.Context, spec dispatch.Spec) (dispatch.Result, error) {
			if s.dispatchEngine == nil {
				return dispatch.Result{}, fmt.Errorf("dispatch engine not configured")
			}
			return s.dispatchEngine.Dispatch(ctx, spec)
		},
	})
	builtins.Register(capReg)
	s.capRegistry = capReg
	s.SetToolRegistry(agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases(), builtins.CapabilitySynonyms()))
}

// SetPermissions wires the permission store and pending-decisions barrier used
// by the SetPermissionMode / GetPermissionMode / Allow|DenyToolCall RPCs.
func (s *Server) SetPermissions(store *agent.PermissionStore, pending *agent.PendingDecisions) {
	s.permStore = store
	s.pendingDecisions = pending
}

// SetMcpManager wires the MCP host manager so the RPC handlers can delegate to it.
func (s *Server) SetMcpManager(m McpManager) { s.mcpManager = m }

// ListMcpServers implements proto.AgentServer — returns a snapshot of all hosted MCP servers.
func (s *Server) ListMcpServers(ctx context.Context, _ *proto.ListMcpServersRequest) (*proto.ListMcpServersResponse, error) {
	out := &proto.ListMcpServersResponse{}
	if s.mcpManager == nil {
		return out, nil
	}
	for _, st := range s.mcpManager.List() {
		out.Servers = append(out.Servers, &proto.McpServerInfo{
			Name: st.Name, State: string(st.State), ToolCount: int32(st.ToolCount), Error: st.Err,
		})
	}
	return out, nil
}

// AddMcpServer implements proto.AgentServer — connects a new MCP server and persists it.
func (s *Server) AddMcpServer(ctx context.Context, req *proto.AddMcpServerRequest) (*proto.AddMcpServerResponse, error) {
	if s.mcpManager == nil {
		return &proto.AddMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	err := s.mcpManager.Add(ctx, req.GetName(), mcphost.ServerConfig{
		Command: req.GetCommand(), Args: req.GetArgs(), Env: req.GetEnv(),
	})
	if err != nil {
		return &proto.AddMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.AddMcpServerResponse{Ok: true}, nil
}

// RemoveMcpServer implements proto.AgentServer — stops an MCP server and removes it from config.
func (s *Server) RemoveMcpServer(ctx context.Context, req *proto.RemoveMcpServerRequest) (*proto.RemoveMcpServerResponse, error) {
	if s.mcpManager == nil {
		return &proto.RemoveMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	if err := s.mcpManager.Remove(ctx, req.GetName()); err != nil {
		return &proto.RemoveMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.RemoveMcpServerResponse{Ok: true}, nil
}

// RestartMcpServer implements proto.AgentServer — tears down and reconnects a hosted MCP server.
func (s *Server) RestartMcpServer(ctx context.Context, req *proto.RestartMcpServerRequest) (*proto.RestartMcpServerResponse, error) {
	if s.mcpManager == nil {
		return &proto.RestartMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	if err := s.mcpManager.Restart(ctx, req.GetName()); err != nil {
		return &proto.RestartMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	// Restart re-lists synchronously, so the post-restart tool count is
	// available immediately from the manager's status snapshot.
	var toolCount int32
	for _, st := range s.mcpManager.List() {
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
func (s *Server) SetCloudLLMProvider(p llm.Provider) { s.cloudLLMProvider = p }

// SetOpenLLMProvider attaches the native-tool-calling local provider (Ollama).
func (s *Server) SetOpenLLMProvider(p llm.Provider) { s.openLLMProvider = p }

// CloudLLMProvider / OpenLLMProvider return the RAW (unwrapped) providers. The
// dispatch engine reads these per-dispatch so a runtime cloud swap is honored,
// and wraps them itself for usage recording — so these must stay unwrapped.
func (s *Server) CloudLLMProvider() llm.Provider { return s.cloudLLMProvider }
func (s *Server) OpenLLMProvider() llm.Provider { return s.openLLMProvider }

// SetUsageSink installs the sink that resolveMainProvider uses to wrap the
// main tool-loop's provider for token-usage recording. The server's stored
// providers stay raw; wrapping happens at hand-off so the dispatch engine can
// read raw providers without double-counting.
func (s *Server) SetUsageSink(fn func(usage.Usage)) { s.usageSink = fn }

// SetSecrets attaches the secrets store used to retrieve profile API keys.
func (s *Server) SetSecrets(st secrets.Store) { s.secrets = st }

// SetupMeridian constructs the local Meridian proxy manager and wires its
// status listener to the event hub so MeridianStatusChanged is broadcast on
// every state transition. logPath is the file Meridian's stdout/stderr is
// teed into (typically ~/.cercano/meridian.log).
//
// Call once at startup, after the event hub is initialised. After this,
// rebuildCloudLocked will Ensure/Stop the proxy as the active profile's
// route field changes.
func (s *Server) SetupMeridian(logPath string) {
	s.meridianMgr = meridian.New(nil, logPath)
	s.meridianMgr.SetStatusListener(func(st meridian.Status) {
		s.broadcastMeridianStatus(st)
	})
}

// Shutdown tears down long-lived subprocess managers the server owns
// (currently: Meridian). Safe to call once at process exit; cheap when
// nothing was started. Does NOT stop the gRPC server itself — that's the
// caller's job (cmd/cercano/main.go uses GracefulStop).
func (s *Server) Shutdown() {
	if s.meridianMgr != nil {
		s.meridianMgr.Stop()
	}
}

// activeCloudModel returns the cloud model from the active profile — the
// authoritative request-time value. Falls back to the legacy CloudModel
// field only when no active profile exists (e.g. local-only configs). All
// code that asks "what cloud model are we using right now?" should go
// through this, not currentConfig.CloudModel directly.
func (s *Server) activeCloudModel() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	for _, p := range s.currentConfig.CloudProfiles {
		if p.Name == s.currentConfig.ActiveCloudProfile {
			return p.Model
		}
	}
	return s.currentConfig.CloudModel
}

// activeProfile returns the configured active cloud profile, or false if none.
func (s *Server) activeProfile() (config.CloudProfile, bool) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	for _, p := range s.currentConfig.CloudProfiles {
		if p.Name == s.currentConfig.ActiveCloudProfile {
			return p, true
		}
	}
	return config.CloudProfile{}, false
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

// persistConfig saves the current config to disk if a configPath is set.
func (s *Server) persistConfig() {
	if s.configPath != "" {
		s.cfgMu.RLock()
		defer s.cfgMu.RUnlock()
		_ = config.Save(s.currentConfig, s.configPath)
	}
}

// installAbsentCloud clears the native cloud provider and points both the
// router and the coordinator's CloudModel at the absent sentinel, so a failed
// rebuild never leaves a half-wired cloud.
func (s *Server) installAbsentCloud(reason string) {
	s.SetCloudLLMProvider(nil)
	absent := legacymodels.NewAbsentCloudProvider(reason)
	s.router.SetCloudProvider(absent)
	if s.coordinator != nil {
		s.coordinator.SetCloudProvider(absent)
	}
}

// rebuildCloud resolves the active profile + its key and rewires BOTH the native
// tool-loop cloud provider and the router/coordinator CloudModel. On any failure
// (no active profile, no key, unsupported flavor, keychain down) it clears the
// native cloud provider and installs the absent-cloud sentinel — the agent keeps
// running with cloud absent.
//
// Thin lock wrapper around rebuildCloudLocked. UpdateConfig (which already
// holds cfgMu write lock) calls the Locked variant directly to avoid a
// re-entrance deadlock.
func (s *Server) rebuildCloud() error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.rebuildCloudLocked()
}

// rebuildCloudLocked is rebuildCloud's body. Caller MUST hold cfgMu write
// lock. Reads ActiveCloudProfile + CloudProfiles, writes CloudModel mirror.
func (s *Server) rebuildCloudLocked() error {
	var p config.CloudProfile
	found := false
	for _, pp := range s.currentConfig.CloudProfiles {
		if pp.Name == s.currentConfig.ActiveCloudProfile {
			p = pp
			found = true
			break
		}
	}
	if !found {
		s.installAbsentCloud("no active cloud profile")
		return fmt.Errorf("no active cloud profile")
	}
	key := ""
	if s.secrets != nil {
		if k, err := s.secrets.Get(p.Name); err == nil {
			key = k
		}
	}
	// If neither a key nor a proxy BaseURL is present the profile cannot
	// authenticate — install the absent sentinel rather than wiring a dead
	// provider. Carve-outs: a proxy BaseURL (Meridian) handles auth with an
	// empty key; and bedrock authenticates via the AWS credential chain, so it
	// legitimately has no keychain key (its failure mode is a missing region).
	if key == "" && p.BaseURL == "" && p.Flavor != cloudfactory.FlavorBedrock {
		s.installAbsentCloud("no API key for profile " + p.Name)
		return fmt.Errorf("no API key for profile %s", p.Name)
	}
	prov, err := cloudfactory.BuildCloudProvider(p, key)
	if err != nil {
		s.installAbsentCloud(err.Error())
		return err
	}
	s.SetCloudLLMProvider(prov)
	mp := agent.NewLLMModelProvider(prov, p.Model)
	s.router.SetCloudProvider(mp)
	if s.coordinator != nil {
		s.coordinator.SetCloudProvider(mp)
	}
	s.currentConfig.CloudModel = p.Model // keep CloudModel reporting consistent
	s.syncMeridianForProfile(p)
	return nil
}

// syncMeridianForProfile starts or stops the managed Meridian proxy based on
// the active profile's Route field. Called at the end of rebuildCloudLocked
// so a profile change (or initial load) keeps the proxy in sync with what
// the cloud route expects.
//
// No-op when SetupMeridian was never called (tests / minimal embeddings).
func (s *Server) syncMeridianForProfile(p config.CloudProfile) {
	if s.meridianMgr == nil {
		return
	}
	if p.Route != "meridian" {
		s.meridianMgr.Stop()
		return
	}
	port := meridianPortFromBaseURL(p.BaseURL)
	s.meridianMgr.Ensure(context.Background(), port)
}

// meridianPortFromBaseURL extracts the TCP port from a profile's BaseURL,
// falling back to 3456 (Meridian's default) on parse failure or missing port.
// Only loopback URLs are sensible here — the manager spawns Meridian locally,
// so a remote BaseURL would mean "talk to someone else's proxy" and is
// effectively unmanaged.
func meridianPortFromBaseURL(baseURL string) int {
	const defaultPort = 3456
	if baseURL == "" {
		return defaultPort
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return defaultPort
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	return defaultPort
}

// RebuildCloud exports rebuildCloud for use by cmd/cercano/main.go at startup.
func (s *Server) RebuildCloud() error { return s.rebuildCloud() }

// GetCloudProfiles implements proto.AgentServer — returns the list of configured cloud profiles.
func (s *Server) GetCloudProfiles(ctx context.Context, req *proto.GetCloudProfilesRequest) (*proto.GetCloudProfilesResponse, error) {
	s.cfgMu.RLock()
	active := s.currentConfig.ActiveCloudProfile
	profiles := append([]config.CloudProfile(nil), s.currentConfig.CloudProfiles...)
	s.cfgMu.RUnlock()

	out := &proto.GetCloudProfilesResponse{Active: active}
	for _, p := range profiles {
		hasKey := false
		if s.secrets != nil {
			if _, err := s.secrets.Get(p.Name); err == nil {
				hasKey = true
			}
		}
		out.Profiles = append(out.Profiles, &proto.CloudProfileInfo{
			Name: p.Name, Flavor: p.Flavor, BaseUrl: p.BaseURL, Model: p.Model, HasKey: hasKey, Backend: p.Backend, Route: p.Route,
		})
	}
	if s.meridianMgr != nil {
		out.MeridianStatus = meridianStatusToProto(s.meridianMgr.Status())
	} else {
		out.MeridianStatus = &proto.MeridianStatus{State: "disabled"}
	}
	return out, nil
}

// SetActiveCloudProfile implements proto.AgentServer — switches the active cloud profile.
func (s *Server) SetActiveCloudProfile(ctx context.Context, req *proto.SetActiveCloudProfileRequest) (*proto.SetActiveCloudProfileResponse, error) {
	s.cfgMu.Lock()
	if _, ok := profileByName(s.currentConfig.CloudProfiles, req.GetName()); !ok {
		s.cfgMu.Unlock()
		return &proto.SetActiveCloudProfileResponse{Ok: false, Error: fmt.Sprintf("no profile %q", req.GetName())}, nil
	}
	s.currentConfig.ActiveCloudProfile = req.GetName()
	s.cfgMu.Unlock()
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
	if s.secrets == nil {
		return &proto.SetCloudProfileKeyResponse{Ok: false, Error: "keychain unavailable"}, nil
	}
	s.cfgMu.RLock()
	_, ok := profileByName(s.currentConfig.CloudProfiles, req.GetName())
	isActive := req.GetName() == s.currentConfig.ActiveCloudProfile
	s.cfgMu.RUnlock()
	if !ok {
		return &proto.SetCloudProfileKeyResponse{Ok: false, Error: fmt.Sprintf("no profile %q", req.GetName())}, nil
	}
	if err := s.secrets.Set(req.GetName(), req.GetApiKey()); err != nil {
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
		BaseURL: req.GetBaseUrl(), Model: req.GetModel(),
	}
	s.cfgMu.Lock()
	replaced := false
	for i, p := range s.currentConfig.CloudProfiles {
		if p.Name == name {
			s.currentConfig.CloudProfiles[i] = np
			replaced = true
			break
		}
	}
	if !replaced {
		s.currentConfig.CloudProfiles = append(s.currentConfig.CloudProfiles, np)
	}
	isActive := name == s.currentConfig.ActiveCloudProfile
	s.cfgMu.Unlock()
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
	s.cfgMu.Lock()
	if _, ok := profileByName(s.currentConfig.CloudProfiles, name); !ok {
		s.cfgMu.Unlock()
		return &proto.RemoveCloudProfileResponse{Ok: false, Error: fmt.Sprintf("no profile %q", name)}, nil
	}
	kept := s.currentConfig.CloudProfiles[:0]
	for _, p := range s.currentConfig.CloudProfiles {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	s.currentConfig.CloudProfiles = kept
	wasActive := s.currentConfig.ActiveCloudProfile == name
	if wasActive {
		s.currentConfig.ActiveCloudProfile = ""
	}
	s.cfgMu.Unlock()

	if s.secrets != nil {
		_ = s.secrets.Delete(name) // best-effort; missing key is not an error
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
func (s *Server) resolveMainProvider() (llm.Provider, bool, bool, error) {
	s.cfgMu.RLock()
	locusMode := s.currentConfig.LocusMode
	s.cfgMu.RUnlock()
	mode, _ := locus.ParseMode(locusMode)
	sel, err := dispatch.Select(mode, dispatch.RoleMain, dispatch.Providers{
		Cloud: s.cloudLLMProvider,
		Open:  s.openLLMProvider,
	})
	if err != nil {
		return nil, false, false, err
	}
	// Wrap the selected provider for "main" token-usage recording at hand-off.
	// The stored providers stay raw (the dispatch engine reads them raw and
	// wraps per-dispatch with its own source), so there's no double-counting.
	prov := usage.Wrap(sel.Provider, "main", sel.IsCloud, s.usageSink)
	return prov, sel.IsCloud, sel.FellBack, nil
}

// SetRuntimeManager attaches the local runtime/dashboard state manager.
func (s *Server) SetRuntimeManager(m localruntime.Manager) {
	s.runtimeManager = m
	s.refreshRuntimeEndpoints()
}

// SetCatalogManager attaches the online-catalog manager so
// ListRuntimeModels can surface Ollama's public library alongside the
// hardcoded catalog + downloaded files, and RefreshOnlineCatalog is
// wired to the manager's Refresh method.
func (s *Server) SetCatalogManager(cm *ollamacatalog.Manager) {
	s.catalogManager = cm
}

// SetRetentionSweeper attaches the background retention sweeper so that
// /config and settings-page changes to retention horizons take effect on the
// next sweep without a restart.
func (s *Server) SetRetentionSweeper(sw *retention.Sweeper) { s.retentionSweeper = sw }

// SetCompactionGenerator attaches the background compaction scheduler so that
// /config compaction-enabled true|false flips it at runtime without a restart.
func (s *Server) SetCompactionGenerator(g *compactiongen.Generator) { s.compactionGen = g }

// NewServer creates a new Agent gRPC server.
func NewServer(a *agent.Agent, openProvider *legacymodels.OpenModelProvider, router RouterCloudUpdater, coordinator *loop.ADKCoordinator, cloudFactory agent.CloudFactory, registry *engine.EngineRegistry) *Server {
	return &Server{
		agent:         a,
		openProvider: openProvider,
		router:        router,
		coordinator:   coordinator,
		cloudFactory:  cloudFactory,
		registry:      registry,
		events:        newEventHub(),
	}
}

// SetConfigPersistence enables config persistence by storing the config path and current state.
func (s *Server) SetConfigPersistence(path string, cfg config.Config) {
	s.configPath = path
	s.cfgMu.Lock()
	s.currentConfig = cfg
	s.cfgMu.Unlock()
	s.refreshRuntimeEndpoints()
}

// LocusMode returns the currently configured Locus Mode (live; reflects
// UpdateConfig). Used by the agent for co-processor tier resolution.
func (s *Server) LocusMode() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.currentConfig.LocusMode
}

// UpdateConfig implements proto.AgentServer — updates runtime config without restart.
func (s *Server) UpdateConfig(ctx context.Context, req *proto.UpdateConfigRequest) (*proto.UpdateConfigResponse, error) {
	// UpdateConfig calls NO cfgMu-locking method (it mutates/reads currentConfig
	// fields directly, calls registry/provider/coordinator methods, broadcasts,
	// applyRuntimeEndpoints, and config.Save — none of which take cfgMu), so the
	// whole body can hold the write lock without risk of a nested-lock deadlock.
	// Config updates are rare, so holding the lock across the registry/provider
	// calls is acceptable.
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	var changes []string

	if req.OllamaUrl != "" {
		u, err := url.ParseRequestURI(req.OllamaUrl)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid ollama_url %q: must be a valid http:// or https:// URL", req.OllamaUrl),
			}, nil
		}
		// Stop any existing health monitor before switching URLs
		if s.healthMonitorCancel != nil {
			s.healthMonitorCancel()
		}

		if s.registry != nil {
			if eng, err := s.registry.GetEngine("ollama"); err == nil {
				if confEng, ok := eng.(engine.ConfigurableEngine); ok {
					confEng.SetBaseURL(req.OllamaUrl)
					// Start health monitor for the new remote endpoint
					monitorCtx, cancel := context.WithCancel(context.Background())
					s.healthMonitorCancel = cancel
					confEng.StartHealthMonitor(monitorCtx, 30*time.Second, 3)
				}
			}
		}

		changes = append(changes, fmt.Sprintf("ollama_url=%s", req.OllamaUrl))
		fmt.Printf("UpdateConfig: Ollama URL set to %s (health monitor started)\n", req.OllamaUrl)
	}

	if req.OpenModel != "" {
		s.openProvider.SetModelName(req.OpenModel)
		changes = append(changes, fmt.Sprintf("local_model=%s", req.OpenModel))
		fmt.Printf("UpdateConfig: Local model set to %s\n", req.OpenModel)
	}

	if req.OpenRuntime != "" {
		if req.OpenRuntime != "ollama" && req.OpenRuntime != "llama_server" {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid local_runtime %q: expected ollama or llama_server", req.OpenRuntime),
			}, nil
		}
		if s.registry == nil {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: "engine registry is not configured",
			}, nil
		}
		eng, err := s.registry.GetEngine(req.OpenRuntime)
		if err != nil {
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
			if err := llamaserver.Detect(ctx, &s.currentConfig.LlamaServer); err != nil {
				if de, ok := err.(*llamaserver.DetectError); ok {
					detectErr = de
				}
				fmt.Printf("UpdateConfig: llama-server detection: %v\n", err)
			} else {
				fmt.Printf("UpdateConfig: llama-server auto-configured — binary=%s default_model=%s\n",
					s.currentConfig.LlamaServer.Binary, s.currentConfig.LlamaServer.DefaultModel)
			}
		}
		model := req.OpenModel
		if model == "" && req.OpenRuntime == "llama_server" {
			model = s.currentConfig.LlamaServer.DefaultModel
		}
		if model == "" {
			model = s.currentConfig.OpenModel
		}
		s.openProvider.SetEngine(eng, model)
		changes = append(changes, fmt.Sprintf("local_runtime=%s", req.OpenRuntime))
		fmt.Printf("UpdateConfig: Local runtime set to %s\n", req.OpenRuntime)
		s.broadcastOpenRuntimeStatus(buildOpenRuntimeStatus(req.OpenRuntime, s.currentConfig, detectErr))
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
		s.currentConfig.Compaction.ElideToolResults = v == "true"
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
		s.currentConfig.Compaction.LossyToolElision = v == "true"
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
		s.currentConfig.Compaction.Enabled = enabled
		// Flip the runtime kill switch. In-flight passes finish; new Schedule
		// calls noop when disabled. Nil-guarded because the server may run
		// without a persistent store (no compGen wired).
		if s.compactionGen != nil {
			s.compactionGen.SetEnabled(enabled)
		}
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
		s.currentConfig.Compaction.Retention.RawRetentionDays = n
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
		s.currentConfig.Compaction.Retention.CompactedRetentionDays = n
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
		s.currentConfig.Compaction.Retention.KeepForever = v == "true"
		changes = append(changes, fmt.Sprintf("keep_forever=%s", v))
		s.broadcastConfigChanged("keep_forever", v)
		fmt.Printf("UpdateConfig: keep_forever set to %s\n", v)
		retentionChanged = true
	}
	// Push the reconciled retention block to the background sweeper so the
	// next sweep uses the new horizons without waiting for a restart.
	if retentionChanged && s.retentionSweeper != nil {
		r := s.currentConfig.Compaction.Retention
		s.retentionSweeper.SetConfig(retention.Config{
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
		// Outer cfgMu write lock is held by UpdateConfig — must not re-lock.
		// Mutate the active profile in place, then drive rebuildCloudLocked.
		activeName := s.currentConfig.ActiveCloudProfile
		idx := -1
		for i, p := range s.currentConfig.CloudProfiles {
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
			s.currentConfig.CloudProfiles[idx].Model = req.CloudModel
		}
		if req.CloudBaseUrl != "" {
			s.currentConfig.CloudProfiles[idx].BaseURL = req.CloudBaseUrl
		}
		profileName := s.currentConfig.CloudProfiles[idx].Name

		// API key goes to the keychain (keyed by profile name), never to
		// the profile struct or the legacy CloudAPIKey field.
		if req.CloudApiKey != "" && s.secrets != nil {
			if err := s.secrets.Set(profileName, req.CloudApiKey); err != nil {
				return &proto.UpdateConfigResponse{
					Success: false,
					Message: fmt.Sprintf("failed to store API key: %v", err),
				}, nil
			}
		}

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
		s.currentConfig.Watchdog.Enabled = req.WatchdogEnabled == "true"
		changes = append(changes, fmt.Sprintf("watchdog_enabled=%s", req.WatchdogEnabled))
		watchdogChanged = true
	}
	if req.WatchdogEcho != "" {
		s.currentConfig.Watchdog.Echo = req.WatchdogEcho == "true"
		changes = append(changes, fmt.Sprintf("watchdog_echo=%s", req.WatchdogEcho))
		watchdogChanged = true
	}
	if req.WatchdogMode == "challenge-and-justify" || req.WatchdogMode == "strict" {
		s.currentConfig.Watchdog.Mode = req.WatchdogMode
		changes = append(changes, "watchdog_mode="+req.WatchdogMode)
		watchdogChanged = true
	}
	if req.WatchdogEscalateAfter != "" {
		if n, err := strconv.Atoi(req.WatchdogEscalateAfter); err == nil && n >= 1 {
			s.currentConfig.Watchdog.EscalateAfter = n
			changes = append(changes, fmt.Sprintf("watchdog_escalate_after=%d", n))
			watchdogChanged = true
		}
	}
	if req.WatchdogChecks != "" {
		if req.WatchdogChecks == "-" {
			s.currentConfig.Watchdog.Checks = []string{}
		} else {
			parts := strings.Split(req.WatchdogChecks, ",")
			checks := make([]string, 0, len(parts))
			for _, p := range parts {
				if trimmed := strings.TrimSpace(p); trimmed != "" {
					checks = append(checks, trimmed)
				}
			}
			s.currentConfig.Watchdog.Checks = checks
		}
		changes = append(changes, "watchdog_checks="+req.WatchdogChecks)
		watchdogChanged = true
	}
	if watchdogChanged {
		// Rebuild the supervisor from the just-applied config. buildWatchdogFrom
		// takes NO lock, so this is safe under the held cfgMu write lock.
		s.watchdog = s.buildWatchdogFrom(s.currentConfig.Watchdog, s.currentConfig.Models)
	}

	if len(changes) == 0 {
		return &proto.UpdateConfigResponse{
			Success: true,
			Message: "no changes requested",
		}, nil
	}

	if req.OllamaUrl != "" {
		s.currentConfig.OllamaURL = req.OllamaUrl
		s.broadcastConfigChanged("ollama_url", req.OllamaUrl)
	}
	if req.OpenModel != "" {
		s.currentConfig.OpenModel = req.OpenModel
		s.broadcastConfigChanged("local_model", req.OpenModel)
	}
	if req.OpenRuntime != "" {
		s.currentConfig.OpenRuntime = req.OpenRuntime
		s.broadcastConfigChanged("local_runtime", req.OpenRuntime)
	}
	if req.CloudProvider != "" {
		s.currentConfig.CloudProvider = req.CloudProvider
		s.broadcastConfigChanged("cloud_provider", req.CloudProvider)
	}
	if req.CloudModel != "" {
		s.currentConfig.CloudModel = req.CloudModel
		s.broadcastConfigChanged("cloud_model", req.CloudModel)
	}
	if req.CloudApiKey != "" {
		s.currentConfig.CloudAPIKey = req.CloudApiKey
		// Presence marker only — never broadcast a raw secret.
		s.broadcastConfigChanged("cloud_api_key", "set")
	}
	if req.CloudBaseUrl != "" {
		s.currentConfig.CloudBaseURL = req.CloudBaseUrl
		s.broadcastConfigChanged("cloud_base_url", req.CloudBaseUrl)
	}
	if req.LocusMode != "" {
		s.currentConfig.LocusMode = req.LocusMode
		s.broadcastConfigChanged("locus_mode", req.LocusMode)
	}
	s.applyRuntimeEndpoints(s.currentConfig)

	// Persist changes to disk
	if s.configPath != "" {
		if err := config.Save(s.currentConfig, s.configPath); err != nil {
			fmt.Printf("UpdateConfig: warning — failed to persist config: %v\n", err)
		}
	}

	return &proto.UpdateConfigResponse{
		Success: true,
		Message: fmt.Sprintf("updated: [%s]", strings.Join(changes, ", ")),
	}, nil
}

// ListConversations implements proto.AgentServer — returns persisted
// conversation summaries for the /history picker.
func (s *Server) ListConversations(ctx context.Context, req *proto.ListConversationsRequest) (*proto.ListConversationsResponse, error) {
	infos, err := s.agent.ListConversations(ctx, req.GetProjectDir(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &proto.ListConversationsResponse{Conversations: make([]*proto.Conversation, 0, len(infos))}
	for _, i := range infos {
		out.Conversations = append(out.Conversations, &proto.Conversation{
			Id:             i.ID,
			Title:          i.Title,
			ProjectDir:     i.ProjectDir,
			Model:          i.Model,
			StartedAt:      i.StartedAt.Unix(),
			LastTurnAt:     i.LastTurnAt.Unix(),
			TurnCount:      int32(i.TurnCount),
			Recap:          i.Recap,
			RecapUpdatedAt: i.RecapUpdatedAt.Unix(),
		})
	}
	return out, nil
}

// GetConversation implements proto.AgentServer — returns a single
// conversation's metadata including its living recap. Lightweight: no turn
// rehydration.
func (s *Server) GetConversation(ctx context.Context, req *proto.GetConversationRequest) (*proto.Conversation, error) {
	i, err := s.agent.GetConversation(ctx, req.GetConversationId())
	if err != nil {
		return nil, err
	}
	return &proto.Conversation{
		Id:             i.ID,
		Title:          i.Title,
		ProjectDir:     i.ProjectDir,
		Model:          i.Model,
		StartedAt:      i.StartedAt.Unix(),
		LastTurnAt:     i.LastTurnAt.Unix(),
		TurnCount:      int32(i.TurnCount),
		Recap:          i.Recap,
		RecapUpdatedAt: i.RecapUpdatedAt.Unix(),
	}, nil
}

// ResumeConversation implements proto.AgentServer — loads persisted turns
// for a conversation, rehydrates the in-memory session store, returns the
// turns so the CLI can render them in scrollback.
func (s *Server) ResumeConversation(ctx context.Context, req *proto.ResumeConversationRequest) (*proto.ResumeConversationResponse, error) {
	turns, err := s.agent.ResumeConversation(ctx, req.GetConversationId())
	if err != nil {
		return nil, err
	}
	out := &proto.ResumeConversationResponse{Turns: make([]*proto.PersistedTurn, 0, len(turns))}
	for _, t := range turns {
		out.Turns = append(out.Turns, &proto.PersistedTurn{
			Id:             t.ID,
			ConversationId: t.ConversationID,
			Role:           t.Role,
			Content:        t.Content,
			TokensIn:       int32(t.TokensIn),
			TokensOut:      int32(t.TokensOut),
			LatencyMs:      int32(t.LatencyMs),
			CreatedAt:      t.CreatedAt.Unix(),
		})
	}
	return out, nil
}

// DeleteConversation implements proto.AgentServer.
func (s *Server) DeleteConversation(ctx context.Context, req *proto.DeleteConversationRequest) (*proto.DeleteConversationResponse, error) {
	if err := s.agent.DeleteConversation(ctx, req.GetConversationId()); err != nil {
		return nil, err
	}
	return &proto.DeleteConversationResponse{Ok: true}, nil
}

// RenameConversation implements proto.AgentServer.
func (s *Server) RenameConversation(ctx context.Context, req *proto.RenameConversationRequest) (*proto.RenameConversationResponse, error) {
	if err := s.agent.RenameConversation(ctx, req.GetConversationId(), req.GetTitle()); err != nil {
		return nil, err
	}
	return &proto.RenameConversationResponse{Ok: true}, nil
}

// ListTools implements proto.AgentServer — enumerates the agent's tool
// registry for the CLI's /tools listing. Returns an empty list when no
// registry was wired (e.g. tests that don't need tools).
func (s *Server) ListTools(ctx context.Context, req *proto.ListToolsRequest) (*proto.ListToolsResponse, error) {
	if s.toolRegistry == nil {
		return &proto.ListToolsResponse{}, nil
	}
	tools := s.toolRegistry.All()
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
	if s.toolRegistry == nil {
		resp.Error = "no tool registry configured"
		return resp, nil
	}
	tool, ok := s.toolRegistry.Get(req.GetName())
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

// estimateRawTokens is a fast len/4 token estimate over the turns' text — used
// for the displayed raw/savings figure so the footer's frequent GetContextUsage
// poll never tokenizes the full uncompacted history with tiktoken.
//
// Uses the LARGER of Content and BlocksJSON per turn, not the sum: BlocksJSON
// is the canonical block-array serialization (what feeds BuildLLMHistory) and
// already contains every text body as a JSON string, so summing it with
// Content double-counts the text portion — inflating the number by ~8% on a
// text-heavy conversation. Content is retained only as the fallback for
// pre-BlocksJSON turns (older rows may have Content but an empty
// content_json column, since it defaulted to the empty string when added).
func estimateRawTokens(turns []conversation.Turn) int {
	n := 0
	for _, t := range turns {
		if len(t.BlocksJSON) > len(t.Content) {
			n += len(t.BlocksJSON)
		} else {
			n += len(t.Content)
		}
	}
	return (n + 3) / 4
}

// GetContextUsage implements proto.AgentServer — reports cumulative token
// usage vs. the active model's context-window size for a conversation.
func (s *Server) GetContextUsage(ctx context.Context, req *proto.GetContextUsageRequest) (*proto.GetContextUsageResponse, error) {
	convID := req.GetConversationId()
	_, max := s.agent.GetContextUsage(ctx, convID)
	sent, raw := 0, 0
	if store := s.agent.PersistentStore(); store != nil && convID != "" {
		if turns, err := store.GetTurns(ctx, convID); err == nil {
			raw = estimateRawTokens(turns)
			state, _ := store.GetCompaction(ctx, convID)
			s.cfgMu.RLock()
			elide := s.currentConfig.Compaction.ElideToolResults
			lossy := s.currentConfig.Compaction.LossyToolElision
			s.cfgMu.RUnlock()
			switch {
			case state.ConsolidatedJSON != "":
				// Compaction has run. Mirror assembleHistory: summarized view
				// plus optional post-elision.
				view, _ := compactor.BuildSendView(turns, state)
				if elide {
					view, _ = compaction.ElideSupersededToolResults(view)
				}
				if lossy {
					view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
				}
				sent = compaction.TotalTokens(contextmeter.Default(), view)
			case elide || lossy:
				// No compaction but some elision is on. The meter must reflect
				// the elided view, not the raw history — otherwise a user
				// turns on a toggle and sees no change even though the model
				// receives less. Cost is one full-history tokenize per poll;
				// acceptable because the elided view is what the request path
				// is already building on every turn.
				view := agent.BuildLLMHistory(turns)
				if elide {
					view, _ = compaction.ElideSupersededToolResults(view)
				}
				if lossy {
					view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
				}
				sent = compaction.TotalTokens(contextmeter.Default(), view)
			default:
				// Fast path: no compaction, no elision. Cheap len/4 estimate
				// is intentional — the footer polls this frequently.
				sent = raw
			}
		}
	}
	var pct float64
	if max > 0 {
		pct = float64(sent) / float64(max)
		if pct > 1 {
			pct = 1
		}
	}
	return &proto.GetContextUsageResponse{
		TokensUsed: int32(sent), ModelMax: int32(max), Percent: pct,
		RawTokens: int32(raw), Compacting: s.agent.IsCompacting(convID),
	}, nil
}

// SuggestNextPrompt implements proto.AgentServer — asks the local co-processor
// for one short follow-up prompt the user might send next, based on the
// conversation's living recap + the tail of recent turns. Degrades to an empty
// response on any failure (missing store, no dispatch engine, provider error,
// empty conversation) — the CLI treats "" as "no suggestion", never surfaces
// a banner. Never routes to the cloud; coproc role only.
func (s *Server) SuggestNextPrompt(ctx context.Context, req *proto.SuggestNextPromptRequest) (*proto.SuggestNextPromptResponse, error) {
	empty := &proto.SuggestNextPromptResponse{}
	convID := req.GetConversationId()
	if convID == "" || s.dispatchEngine == nil {
		return empty, nil
	}
	store := s.agent.PersistentStore()
	if store == nil {
		return empty, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil || len(turns) == 0 {
		return empty, nil
	}
	// Pull the recap for background; fall back to just the turn tail when
	// none has been consolidated yet.
	recap := ""
	if info, err := store.Get(ctx, convID); err == nil {
		recap = info.Recap
	}
	// Last 6 turns give the coproc immediate context without ballooning the
	// prompt on long conversations. Recap covers the deeper history.
	tailN := 6
	if len(turns) < tailN {
		tailN = len(turns)
	}
	var tail strings.Builder
	for _, t := range turns[len(turns)-tailN:] {
		content := strings.TrimSpace(t.Content)
		if content == "" {
			continue
		}
		if len(content) > 400 {
			content = content[:400] + "…"
		}
		fmt.Fprintf(&tail, "[%s]\n%s\n\n", t.Role, content)
	}
	var promptB strings.Builder
	promptB.WriteString("You are helping predict what a user might reasonably ask next in an ongoing conversation with an AI coding assistant. ")
	promptB.WriteString("Output ONE short natural next prompt the user might send, under 80 characters. ")
	promptB.WriteString("Rules: output ONLY the prompt text; no quotes, no formatting, no leading punctuation, no commentary, no labels.\n\n")
	if recap != "" {
		promptB.WriteString("Recap of the conversation so far:\n")
		promptB.WriteString(recap)
		promptB.WriteString("\n\n")
	}
	promptB.WriteString("Most recent turns:\n")
	promptB.WriteString(tail.String())
	promptB.WriteString("\nNext prompt:")

	res, err := s.dispatchEngine.Dispatch(ctx, dispatch.Spec{
		Mode:        dispatch.OneShot,
		Role:        dispatch.RoleCoproc,
		Prompt:      promptB.String(),
		Source:      "suggest_next_prompt",
		RecordUsage: true,
	})
	if err != nil {
		return empty, nil
	}
	return &proto.SuggestNextPromptResponse{Suggestion: sanitizeSuggestion(res.Text)}, nil
}

// sanitizeSuggestion normalizes a coproc's suggestion text: single line, no
// surrounding quotes, capped at 80 characters. Belt-and-suspenders against
// models that ignore the "no formatting" rule.
func sanitizeSuggestion(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	// Strip matched surrounding quote characters (", ', `).
	for len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') || (first == '`' && last == '`') {
			s = strings.TrimSpace(s[1 : len(s)-1])
			continue
		}
		break
	}
	// Drop leading list/label punctuation the model might inject anyway.
	s = strings.TrimLeft(s, "-*•> ")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// GetCompactionState implements proto.AgentServer — the compaction summary +
// frozen/live split for the /c viewer.
func (s *Server) GetCompactionState(ctx context.Context, req *proto.GetCompactionStateRequest) (*proto.GetCompactionStateResponse, error) {
	convID := req.GetConversationId()
	out := &proto.GetCompactionStateResponse{Compacting: s.agent.IsCompacting(convID)}
	store := s.agent.PersistentStore()
	if store == nil || convID == "" {
		return out, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return out, nil
	}
	state, _ := store.GetCompaction(ctx, convID)
	view, _ := compactor.BuildSendView(turns, state)
	out.SentTokens = int32(compaction.TotalTokens(contextmeter.Default(), view))
	out.RawTokens = int32(estimateRawTokens(turns))
	out.FrozenThrough = state.FrozenThrough
	for _, t := range turns {
		if t.CreatedAt.Unix() <= state.FrozenThrough {
			out.FrozenTurns++
		} else {
			out.LiveTurns++
		}
	}
	if state.SegmentSummariesJSON != "" {
		var segs []compaction.StructuredSummary
		if json.Unmarshal([]byte(state.SegmentSummariesJSON), &segs) == nil {
			out.CompactedSegments = int32(len(segs))
		}
	}
	if state.ConsolidatedJSON != "" {
		var cs compaction.StructuredSummary
		if json.Unmarshal([]byte(state.ConsolidatedJSON), &cs) == nil {
			out.ConsolidatedSummary = cs.RenderBlock().Text
		}
	}
	return out, nil
}

// ExportContext implements proto.AgentServer — the full uncapped raw history as
// a JSON []llm.Message.
func (s *Server) ExportContext(ctx context.Context, req *proto.ExportContextRequest) (*proto.ExportContextResponse, error) {
	store := s.agent.PersistentStore()
	if store == nil || req.GetConversationId() == "" {
		return &proto.ExportContextResponse{}, nil
	}
	turns, err := store.GetTurns(ctx, req.GetConversationId())
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(agent.BuildLLMHistory(turns))
	if err != nil {
		return nil, err
	}
	return &proto.ExportContextResponse{Json: string(b)}, nil
}

// GetConfig implements proto.AgentServer — reports the current runtime config
// without exposing the literal API key. cloud_state is derived from the active
// cloud provider's Name() ("NONE" → "absent", everything else → "ok").
func (s *Server) GetConfig(ctx context.Context, req *proto.GetConfigRequest) (*proto.GetConfigResponse, error) {
	state := "absent"
	if s.router != nil {
		if cp, ok := s.router.GetModelProviders()["CloudModel"]; ok && cp != nil {
			if cp.Name() != "NONE" {
				state = "ok"
			}
		}
	}
	s.cfgMu.RLock()
	cfg := s.currentConfig
	s.cfgMu.RUnlock()
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
		OpenModel:             cfg.OpenModel,
		EmbeddingModel:         cfg.EmbeddingModel,
		CloudProvider:          cloudProvider,
		CloudModel:             cloudModel,
		CloudBaseUrl:           cloudBaseURL,
		CloudApiKeySet:         cfg.CloudAPIKey != "",
		CloudState:             state,
		Port:                   cfg.Port,
		OpenRuntime:           cfg.OpenRuntime,
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
	}, nil
}

// ListModels implements proto.AgentServer — returns available models from the active local runtime.
func (s *Server) ListModels(ctx context.Context, req *proto.ListModelsRequest) (*proto.ListModelsResponse, error) {
	if s.registry == nil {
		return nil, fmt.Errorf("registry not configured")
	}
	s.cfgMu.RLock()
	runtimeName := s.currentConfig.OpenRuntime
	s.cfgMu.RUnlock()
	if runtimeName == "" {
		runtimeName = "ollama"
	}
	eng, err := s.registry.GetEngine(runtimeName)
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
	if s.runtimeManager == nil {
		return &proto.GetRuntimeStatusResponse{}, nil
	}
	status, err := s.runtimeManager.Status(ctx)
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
	if s.runtimeManager == nil {
		return &proto.ListRuntimeModelsResponse{}, nil
	}
	models, err := s.runtimeManager.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	resp := &proto.ListRuntimeModelsResponse{Models: mapRuntimeModels(models)}
	if s.catalogManager != nil {
		online := s.catalogManager.Models()
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
		if fa := s.catalogManager.FetchedAt(); !fa.IsZero() {
			resp.CatalogUpdatedAt = fa.UTC().Format(time.RFC3339)
		}
	}
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
	if s.catalogManager == nil {
		return &proto.RefreshOnlineCatalogResponse{Error: "online catalog not configured"}, nil
	}
	if err := s.catalogManager.Refresh(ctx); err != nil {
		// Refresh failure leaves the previous cache in place — surface
		// the error so the CLI can render it, but keep the timestamp
		// pointing at the most-recent SUCCESSFUL fetch so users can
		// still tell how stale the current view is.
		resp := &proto.RefreshOnlineCatalogResponse{Error: err.Error()}
		if fa := s.catalogManager.FetchedAt(); !fa.IsZero() {
			resp.CatalogUpdatedAt = fa.UTC().Format(time.RFC3339)
		}
		return resp, nil
	}
	return &proto.RefreshOnlineCatalogResponse{
		CatalogUpdatedAt: s.catalogManager.FetchedAt().UTC().Format(time.RFC3339),
		ModelCount:       int32(len(s.catalogManager.Models())),
	}, nil
}

// ListRuntimeEndpoints implements proto.AgentServer.
func (s *Server) ListRuntimeEndpoints(ctx context.Context, req *proto.ListRuntimeEndpointsRequest) (*proto.ListRuntimeEndpointsResponse, error) {
	if s.runtimeManager == nil {
		return &proto.ListRuntimeEndpointsResponse{}, nil
	}
	endpoints, err := s.runtimeManager.Endpoints(ctx)
	if err != nil {
		return nil, err
	}
	return &proto.ListRuntimeEndpointsResponse{Endpoints: mapRuntimeEndpoints(endpoints)}, nil
}

// StartRuntimeModel implements proto.AgentServer.
func (s *Server) StartRuntimeModel(ctx context.Context, req *proto.StartRuntimeModelRequest) (*proto.StartRuntimeModelResponse, error) {
	if s.runtimeManager == nil {
		return &proto.StartRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	instance, err := s.runtimeManager.Start(ctx, localruntime.StartRequest{
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
	if s.runtimeManager == nil {
		return &proto.StopRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	if err := s.runtimeManager.Stop(ctx, localruntime.StopRequest{InstanceID: req.GetInstanceId()}); err != nil {
		return &proto.StopRuntimeModelResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.StopRuntimeModelResponse{Ok: true}, nil
}

// RestartRuntime implements proto.AgentServer.
func (s *Server) RestartRuntime(ctx context.Context, req *proto.RestartRuntimeRequest) (*proto.RestartRuntimeResponse, error) {
	if s.runtimeManager == nil {
		return &proto.RestartRuntimeResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	instance, err := s.runtimeManager.Restart(ctx, localruntime.RestartRequest{
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

func (s *Server) DownloadRuntimeModel(ctx context.Context, req *proto.DownloadRuntimeModelRequest) (*proto.DownloadRuntimeModelResponse, error) {
	if s.runtimeManager == nil {
		return &proto.DownloadRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	if ref := normalizeOllamaRef(req.GetOllamaRef()); ref != "" {
		// Only the concrete InMemoryManager supports enrolment. If a
		// future alternative implementation is wired in, this branch
		// is a no-op and the download will fall back to the provider
		// lookup below (which will fail cleanly with "not found").
		if imm, ok := s.runtimeManager.(*localruntime.InMemoryManager); ok {
			imm.EnrollDownload(localruntime.ModelRecord{
				ID:            req.GetModelId(),
				Runtime:       req.GetRuntime(),
				OllamaRef:     ref,
				DownloadState: "not_downloaded",
				Format:        "gguf",
				SupportsChat:  true,
			})
		}
	}
	model, err := s.runtimeManager.DownloadModel(ctx, localruntime.DownloadRequest{
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
	if s.runtimeManager == nil {
		return &proto.CancelRuntimeModelDownloadResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	model, err := s.runtimeManager.CancelDownload(ctx, localruntime.DownloadRequest{
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
	if s.runtimeManager == nil {
		return &proto.DeleteRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
	}
	if err := s.runtimeManager.DeleteModel(ctx, localruntime.DeleteModelRequest{
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
	if s.runtimeManager == nil {
		return nil
	}
	logs, err := s.runtimeManager.Logs(stream.Context(), localruntime.LogRequest{
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

// refreshRuntimeEndpoints snapshots the current config under the read lock,
// releases it, then pushes the derived endpoints. It must NOT be called while
// cfgMu is already held — callers that hold the lock (UpdateConfig) call
// applyRuntimeEndpoints directly with the config they already have.
func (s *Server) refreshRuntimeEndpoints() {
	if s.runtimeManager == nil {
		return
	}
	s.cfgMu.RLock()
	cfg := s.currentConfig
	s.cfgMu.RUnlock()
	s.applyRuntimeEndpoints(cfg)
}

// applyRuntimeEndpoints derives and pushes runtime endpoints from the given
// config snapshot. Lock-free: the caller owns lock discipline.
func (s *Server) applyRuntimeEndpoints(cfg config.Config) {
	if s.runtimeManager == nil {
		return
	}
	s.runtimeManager.SetEndpoints(localruntime.EndpointsFromConfig(cfg))
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

	if (s.cloudLLMProvider != nil || s.openLLMProvider != nil) && s.toolRegistry != nil {
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
	return buildToolLoopSystem(env, steering, directorySnapshot(workDir, 80), s.loadProjectContext(workDir))
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

// loadProjectContext returns the project's .cercano/context.md content, or "" if
// no loader is wired or none exists. Nil-safe.
func (s *Server) loadProjectContext(workDir string) string {
	if s.contextLoader == nil || workDir == "" {
		return ""
	}
	c, _ := s.contextLoader.Load(workDir)
	return c
}

// streamProcessRequestWithToolLoop drives the native tool-calling loop and
// emits per-event stream payloads. Used when a layered LLM provider has been
// wired via SetCloudLLMProvider.
func (s *Server) streamProcessRequestWithToolLoop(req *proto.ProcessRequestRequest, stream proto.Agent_StreamProcessRequestServer) error {
	ctx := stream.Context()

	// F2: propagate WorkDir into tool execution. Tools that touch the
	// filesystem (run_command, read_file, git_status) read os.Getwd when
	// no explicit cwd is passed in their args. Chdir + restore makes them
	// honor the client-supplied project directory. The legacy path passes
	// WorkDir as a string param to the coordinator/provider but does NOT
	// thread it into agenttools — so neither path covers VS Code / Zed
	// clients properly today. This is the simplest V1 fix.
	if req.GetWorkDir() != "" {
		if prev, err := os.Getwd(); err == nil {
			if err := os.Chdir(req.GetWorkDir()); err == nil {
				defer os.Chdir(prev)
			} else {
				fmt.Fprintf(os.Stderr, "[tool-loop] chdir(%s) failed: %v\n", req.GetWorkDir(), err)
			}
		}
	}

	sink := func(ev agent.LoopEvent) {
		switch ev.Kind {
		case agent.LoopToolUseStart:
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_ToolUseStart{
					ToolUseStart: &proto.ToolUseStart{
						ToolUseId: ev.ToolUseID,
						ToolName:  ev.ToolName,
					},
				},
			})
		case agent.LoopToolUseStop:
			// Per-tool trace to the server log so a runaway/looping turn is
			// diagnosable after the fact (which tools, what args, in what order).
			fmt.Fprintf(os.Stderr, "[tool-loop] call %s args=%s\n", ev.ToolName, ev.ArgsJSON)
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_ToolUseStop{
					ToolUseStop: &proto.ToolUseStop{
						ToolUseId:   ev.ToolUseID,
						ArgsSummary: ev.ArgsJSON,
					},
				},
			})
		case agent.LoopToolExecStart:
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_ToolExecStart{
					ToolExecStart: &proto.ToolExecStart{
						ToolUseId: ev.ToolUseID,
					},
				},
			})
		case agent.LoopToolExecComplete:
			fmt.Fprintf(os.Stderr, "[tool-loop]   -> %s (err=%v) %s\n", ev.Summary, ev.IsError, ev.Detail)
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_ToolExecComplete{
					ToolExecComplete: &proto.ToolExecComplete{
						ToolUseId: ev.ToolUseID,
						Summary:   ev.Summary,
						Detail:    ev.Detail,
						IsError:   ev.IsError,
					},
				},
			})
		case agent.LoopWatchdogChallenge, agent.LoopWatchdogBlock:
			kind := "challenge"
			if ev.Kind == agent.LoopWatchdogBlock {
				kind = "block"
			}
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_WatchdogEvent{
					WatchdogEvent: &proto.WatchdogEvent{
						Kind: kind, Protocol: ev.Detail, Text: ev.Summary,
					},
				},
			})
		case agent.LoopWatchdogEcho:
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_WatchdogEvent{
					WatchdogEvent: &proto.WatchdogEvent{
						Kind: "echo", Text: ev.Summary, Thread: ev.ToolName,
					},
				},
			})
		}
	}

	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		if s.pendingDecisions == nil {
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
		d, err := s.pendingDecisions.Wait(ctx, toolUseID)
		if err != nil {
			return false, err
		}
		if d.Allow && d.Persist && s.permStore != nil {
			if tool, ok := s.toolRegistry.Get(name); ok && agenttools.OriginOf(tool) == agenttools.OriginMCP {
				if err := s.permStore.AddMCPAllow(name); err != nil {
					fmt.Fprintf(os.Stderr, "[mcp] persist always-allow for %s: %v\n", name, err)
				}
			}
		}
		return d.Allow, nil
	}

	ctx = anthropic.WithSessionID(ctx, req.GetConversationId())

	// Resolve the provider per the active Locus Mode.
	provider, isCloud, fellBack, err := s.resolveMainProvider()
	if err != nil {
		// *_only mode with its required tier unavailable — hard fail, no silent cross.
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_FinalResponse{
				FinalResponse: &proto.ProcessRequestResponse{Output: "Locus: " + err.Error()},
			},
		})
	}
	if fellBack {
		stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_Progress{
				Progress: &proto.ProgressUpdate{
					Message: fmt.Sprintf("⚠ preferred tier unavailable — falling back to %s (%s)", provider.Name(), s.mainModelFor(isCloud)),
				},
			},
		})
	}

	// Announce the true route so the client can show the correct engine badge.
	stream.Send(&proto.StreamProcessResponse{
		Payload: &proto.StreamProcessResponse_RouteSelected{
			RouteSelected: &proto.RouteSelected{
				Model:   s.mainModelFor(isCloud),
				IsCloud: isCloud,
			},
		},
	})

	var convHistory []llm.Message
	if store := s.agent.PersistentStore(); store != nil && req.GetConversationId() != "" {
		convHistory = s.assembleHistory(ctx, store, req.GetConversationId())
	}

	// Crash-resilient persistence. Persist the USER turn up front (before any LLM
	// call) so an interruption — panic, kill, restart, power loss — can never lose
	// the prompt. The rest of the turn is persisted incrementally as the loop
	// produces each assistant / tool-result message (onTurn → OnTurnComplete), so
	// a crash mid-turn loses at most the reply currently streaming, not the turn.
	// (On the rare cross-tier fallback retry below, a failed attempt's already-
	// persisted assistant turns may be redundantly re-persisted — non-destructive;
	// the user turn is written once here and never via onTurn.)
	convID := req.GetConversationId()
	persistEnabled := s.agent != nil && convID != "" && s.agent.PersistentStore() != nil
	if persistEnabled {
		if err := s.agent.PersistentStore().EnsureConversation(ctx, convID, req.GetWorkDir(), s.mainModelFor(isCloud)); err != nil {
			fmt.Fprintf(os.Stderr, "[tool-loop] EnsureConversation(%s) failed: %v\n", convID, err)
			persistEnabled = false
		} else {
			s.persistTurn(ctx, convID, agent.UserMessage(req.GetInput(), mapInlineImages(req.GetImages())))
		}
	}
	var onTurn func(m llm.Message)
	if persistEnabled {
		onTurn = func(m llm.Message) { s.persistTurn(ctx, convID, m) }
	}

	onTextDelta := func(t string) {
		stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_TokenDelta{
				TokenDelta: &proto.TokenDelta{Content: t},
			},
		})
	}

	// Watchdog wiring (default-OFF: s.watchdog == nil ⇒ unchanged behavior).
	// Per-request gate bound to this conversation + a per-request registry that
	// augments the base tools with the conversation-scoped justify tool.
	gateRegistry := s.toolRegistry
	var wdGate agent.WatchdogGate
	var wdTurnEnd agent.WatchdogTurnEnd
	s.cfgMu.RLock()
	wd := s.watchdog
	s.cfgMu.RUnlock()
	if wd != nil {
		wdGate = func(ctx context.Context, toolName string, args json.RawMessage, transcript []llm.Message) agent.WatchdogDecision {
			d := wd.Gate(ctx, convID, watchdog.Action{Kind: "tool_call", ToolName: toolName, ToolArgs: args, Transcript: transcript})
			return agent.WatchdogDecision{Action: d.Action, Protocol: d.Protocol, Challenge: d.Challenge, Revise: d.Revise}
		}
		wdTurnEnd = func(ctx context.Context, finalText string, transcript []llm.Message) agent.WatchdogDecision {
			d := wd.Gate(ctx, convID, watchdog.Action{Kind: "turn_end", Text: finalText, Transcript: transcript})
			return agent.WatchdogDecision{Action: d.Action, Protocol: d.Protocol, Challenge: d.Challenge, Revise: d.Revise}
		}
		reg := agenttools.NewRegistry()
		for _, t := range s.toolRegistry.All() {
			_ = reg.Register(t)
		}
		_ = reg.Register(wd.JustifyTool(convID))
		gateRegistry = reg

		// Echo forwarding is per-turn: the interactive server processes one turn
		// at a time (turns don't overlap), so setting echo on the shared watchdog
		// here routes its interventions to THIS turn's sink safely. Full
		// multi-conversation echo isolation is a follow-on.
		s.cfgMu.RLock()
		echoOn := s.currentConfig.Watchdog.Echo
		s.cfgMu.RUnlock()
		if echoOn {
			wd.SetEcho(func(thread, text string) {
				sink(agent.LoopEvent{Kind: agent.LoopWatchdogEcho, ToolName: thread, Summary: text})
			})
		}
	}

	result, loopErr := s.runMainLoop(ctx, req, provider, isCloud, sink, requester, convHistory, onTextDelta, onTurn, wdGate, wdTurnEnd, gateRegistry)
	if loopErr != nil {
		s.cfgMu.RLock()
		locusMode := s.currentConfig.LocusMode
		s.cfgMu.RUnlock()
		mode, _ := locus.ParseMode(locusMode)
		res := mode.Main()
		fbProv := s.cloudLLMProvider
		fbCloud := true
		if res.Fallback == locus.TierLocal {
			fbProv, fbCloud = s.openLLMProvider, false
		}
		if !fellBack && res.CrossAllowed && fbProv != nil {
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_Progress{
					Progress: &proto.ProgressUpdate{
						Message: fmt.Sprintf("⚠ %s failed (%v) — retrying on %s", provider.Name(), loopErr, fbProv.Name()),
					},
				},
			})
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_RouteSelected{
					RouteSelected: &proto.RouteSelected{Model: s.mainModelFor(fbCloud), IsCloud: fbCloud},
				},
			})
			provider = fbProv
			isCloud = fbCloud
			result, loopErr = s.runMainLoop(ctx, req, fbProv, fbCloud, sink, requester, convHistory, onTextDelta, onTurn, wdGate, wdTurnEnd, gateRegistry)
		}
		if loopErr != nil {
			return fmt.Errorf("tool loop error: %w", loopErr)
		}
	}

	// Turns were persisted incrementally above (user turn up front + each
	// assistant/tool message via onTurn). Now that the turn is complete, refresh
	// the living recap and schedule background compaction.
	if persistEnabled {
		s.agent.ScheduleRecap(convID)
		s.agent.ScheduleCompaction(convID)
	}
	s.agent.RecordContextUsage(req.GetConversationId(), s.mainModelFor(isCloud),
		result.InputTokens, result.OutputTokens)

	return stream.Send(&proto.StreamProcessResponse{
		Payload: &proto.StreamProcessResponse_FinalResponse{
			FinalResponse: &proto.ProcessRequestResponse{
				Output: strings.ToValidUTF8(result.FinalText, "�"),
				RoutingMetadata: &proto.RoutingMetadata{
					ModelName: provider.Name(),
				},
				// Carry the turn's token counts so the CLI's "last turn" footer
				// isn't stuck at 0 — same values RecordContextUsage just stored.
				InputTokens:  int32(result.InputTokens),
				OutputTokens: int32(result.OutputTokens),
			},
		},
	})
}

// mainModelFor returns the configured model name for the active tier. Cloud
// reads from the active profile (the single source of truth — see
// activeCloudModel) so a profile-model change propagates without restart.
func (s *Server) mainModelFor(isCloud bool) string {
	if isCloud {
		return s.activeCloudModel()
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.currentConfig.OpenModel
}

// runMainLoop drives the native tool-loop on the given provider/tier.
// Factored out so Task 6 (fallback) can reuse it without duplicating the
// RunToolLoop call site.
func (s *Server) runMainLoop(
	ctx context.Context,
	req *proto.ProcessRequestRequest,
	provider llm.Provider,
	isCloud bool,
	sink func(agent.LoopEvent),
	requester func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error),
	convHistory []llm.Message,
	onTextDelta func(string),
	onTurn func(m llm.Message),
	watchdogGate agent.WatchdogGate,
	watchdogTurnEnd agent.WatchdogTurnEnd,
	registry *agenttools.Registry,
) (agent.ToolLoopResult, error) {
	return agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:            provider,
		Registry:            registry,
		Permissions:         s.permStore,
		UserInput:           req.GetInput(),
		Images:              mapInlineImages(req.GetImages()),
		Model:               s.mainModelFor(isCloud),
		System:              s.buildSystemPrompt(req.GetWorkDir()),
		EventSink:           sink,
		PermissionRequester: requester,
		ConvHistory:         convHistory,
		OnTextDelta:         onTextDelta,
		OnTurnComplete:      onTurn,
		WatchdogGate:        watchdogGate,
		WatchdogTurnEnd:     watchdogTurnEnd,
	})
}

// persistTurn writes one conversation turn (any role) to the persistent store,
// with BlocksJSON and concatenated text Content. Best-effort: store errors are
// logged but never surfaced, so a write failure can't abort the turn. Called
// incrementally as the tool loop produces each message (crash resilience).
func (s *Server) persistTurn(ctx context.Context, convID string, m llm.Message) {
	if s.agent == nil || convID == "" {
		return
	}
	store := s.agent.PersistentStore()
	if store == nil {
		return
	}
	blocksJSON, err := json.Marshal(m.Blocks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] marshal blocks failed: %v\n", err)
		return
	}
	var text string
	for _, b := range m.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	role := string(m.Role)
	if err := store.Append(ctx, conversation.Turn{
		ConversationID: convID,
		Role:           role,
		Content:        text,
		BlocksJSON:     string(blocksJSON),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] Append(%s, %s) failed: %v\n", role, convID, err)
	}
}

// assembleHistory builds the conversation history to send: the compacted view
// (consolidated summary + live tail) when compaction state exists, else the full
// history. If the assembled history exceeds the hard-override fraction of the
// model's max context, it schedules a background compaction pass and degrades
// the view with LLM-free elision and front-drop rather than blocking.
func (s *Server) assembleHistory(ctx context.Context, store conversation.Store, convID string) []llm.Message {
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] GetTurns(%s) failed: %v\n", convID, err)
		return nil
	}
	state, _ := store.GetCompaction(ctx, convID)
	view, _ := compactor.BuildSendView(turns, state)

	s.cfgMu.RLock()
	compactionCfg := s.currentConfig.Compaction
	s.cfgMu.RUnlock()
	cloudModel := s.activeCloudModel()
	pct := compactionCfg.HardOverridePct
	if compactionCfg.Enabled && pct > 0 {
		hardLimit := int(float64(contextmeter.ModelMax(cloudModel)) * pct)
		if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
			// Never compact inline — kick the background generator (debounced,
			// deduped, timeout-bounded) and bring THIS turn under the limit with
			// LLM-free steps only.
			s.agent.ScheduleCompaction(convID)
			pre := compaction.TotalTokens(contextmeter.Default(), view)
			view, _ = compaction.ElideSupersededToolResults(view)
			if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
				view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
			}
			if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
				preserve := 0
				if state.ConsolidatedJSON != "" {
					preserve = 1 // keep the consolidated-summary preamble
				}
				var dropped int
				view, dropped = compaction.TruncateOldestToFit(view, contextmeter.Default(), hardLimit, preserve)
				fmt.Fprintf(os.Stderr, "[compaction] hard-override %s: %d tokens > limit %d — truncated %d oldest messages (background pass scheduled)\n",
					convID, pre, hardLimit, dropped)
			} else {
				fmt.Fprintf(os.Stderr, "[compaction] hard-override %s: %d tokens > limit %d — elision brought it under (background pass scheduled)\n",
					convID, pre, hardLimit)
			}
		}
	}
	// Mechanical superseded-tool-result dedup. LLM-free, lossless, and safe to
	// apply on top of either a summarized view or the raw history — running it
	// twice is idempotent.
	if compactionCfg.ElideToolResults {
		view, _ = compaction.ElideSupersededToolResults(view)
	}
	// Recency-window elision. Stubs older tool_result content down to a marker;
	// keeps the last N intact. Applied after byte-identical dedup because the
	// two are complementary — the identical-dedup catches literal duplicates
	// among the kept N; the recency policy handles the long tail.
	if compactionCfg.LossyToolElision {
		view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
	}
	// Final pairing repair: idempotent on a valid view, and insurance for the
	// one degrade edge (a lone surviving tool_result after truncation) that
	// would otherwise reach the provider as an invalid message array.
	return llm.RepairPairing(view)
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
		DirectOpen:    req.DirectOpen,
		ModelOverride:  req.ModelOverride,
		Coproc:         req.Coproc,
		Images:         mapInlineImages(req.GetImages()),
	}
}

// SetPermissionMode implements proto.AgentServer.
func (s *Server) SetPermissionMode(ctx context.Context, req *proto.SetPermissionModeRequest) (*proto.SetPermissionModeResponse, error) {
	if s.permStore == nil {
		return &proto.SetPermissionModeResponse{Ok: false, Error: "permission store not configured"}, nil
	}
	m, err := agent.ParseMode(req.GetMode())
	if err != nil {
		return &proto.SetPermissionModeResponse{Ok: false, Error: err.Error()}, nil
	}
	if err := s.permStore.SetMode(m); err != nil {
		return &proto.SetPermissionModeResponse{Ok: false, Error: err.Error()}, nil
	}
	// Push to every connected client so their status bars update reactively.
	// The file watcher also fires for this same write; broadcastPermissionMode
	// dedupes by value so the two paths collapse to one event.
	s.broadcastPermissionMode(string(m))
	return &proto.SetPermissionModeResponse{Ok: true}, nil
}

// GetPermissionMode implements proto.AgentServer.
func (s *Server) GetPermissionMode(ctx context.Context, req *proto.GetPermissionModeRequest) (*proto.GetPermissionModeResponse, error) {
	if s.permStore == nil {
		return &proto.GetPermissionModeResponse{Mode: string(agent.ModePermissive)}, nil
	}
	return &proto.GetPermissionModeResponse{Mode: string(s.permStore.Mode())}, nil
}

// AllowToolCall implements proto.AgentServer.
func (s *Server) AllowToolCall(ctx context.Context, req *proto.AllowToolCallRequest) (*proto.AllowToolCallResponse, error) {
	if s.pendingDecisions == nil {
		return &proto.AllowToolCallResponse{Ok: false}, nil
	}
	ok := s.pendingDecisions.Resolve(req.GetToolUseId(), agent.Decision{Allow: true, Persist: req.GetPersist()})
	return &proto.AllowToolCallResponse{Ok: ok}, nil
}

// DenyToolCall implements proto.AgentServer.
func (s *Server) DenyToolCall(ctx context.Context, req *proto.DenyToolCallRequest) (*proto.DenyToolCallResponse, error) {
	if s.pendingDecisions == nil {
		return &proto.DenyToolCallResponse{Ok: false}, nil
	}
	ok := s.pendingDecisions.Resolve(req.GetToolUseId(), agent.Decision{Allow: false})
	return &proto.DenyToolCallResponse{Ok: ok}, nil
}

// GetProviderCapabilities implements proto.AgentServer.
func (s *Server) GetProviderCapabilities(ctx context.Context, req *proto.GetProviderCapabilitiesRequest) (*proto.GetProviderCapabilitiesResponse, error) {
	if s.cloudLLMProvider == nil {
		return &proto.GetProviderCapabilitiesResponse{
			SupportsTools:         true,
			SupportsParallelTools: true,
			SupportsCaching:       true,
			SupportsVision:        true,
			MaxToolsPerCall:       0,
		}, nil
	}
	c := s.cloudLLMProvider.Capabilities()
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

	if s.registry != nil {
		if eng, err := s.registry.GetEngine("ollama"); err == nil {
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
