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
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/anthropic"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/loop"
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

// Server is the gRPC server for the Agent service.
type Server struct {
	proto.UnimplementedAgentServer
	agent               *agent.Agent
	localProvider       *legacymodels.LocalModelProvider
	router              RouterCloudUpdater
	coordinator         *loop.ADKCoordinator
	cloudFactory        agent.CloudFactory
	registry            *engine.EngineRegistry
	healthMonitorCancel context.CancelFunc // cancel function for the active health monitor
	configPath          string             // path to config.yaml for persistence
	currentConfig       config.Config      // current config state for persistence
	toolRegistry        *agenttools.Registry
	permStore           *agent.PermissionStore
	pendingDecisions    *agent.PendingDecisions
	cloudLLMProvider    llm.Provider
	localLLMProvider    llm.Provider // native-tool-loop local provider (Ollama)
	runtimeManager      localruntime.Manager
	contextLoader       *projectctx.Loader

	events        *eventHub  // server->client push fan-out (SubscribeEvents)
	permBcastMu   sync.Mutex // guards lastBcastMode
	lastBcastMode string     // last permission mode broadcast; dedupes file-watcher vs SetMode
}

// SetContextLoader wires the project-context loader so the native tool-loop can
// include .cercano/context.md (and the working directory) in its system prompt.
func (s *Server) SetContextLoader(l *projectctx.Loader) { s.contextLoader = l }

// SetToolRegistry attaches the agent's tool registry. The CLI's /tools and
// /tool commands route through ListTools / InvokeTool RPCs to it.
func (s *Server) SetToolRegistry(r *agenttools.Registry) { s.toolRegistry = r }

// SetPermissions wires the permission store and pending-decisions barrier used
// by the SetPermissionMode / GetPermissionMode / Allow|DenyToolCall RPCs.
func (s *Server) SetPermissions(store *agent.PermissionStore, pending *agent.PendingDecisions) {
	s.permStore = store
	s.pendingDecisions = pending
}

// SetCloudLLMProvider attaches the native-tool-calling cloud provider used by
// GetProviderCapabilities. Optional — when nil, GetProviderCapabilities falls
// back to a hardcoded Anthropic-shaped capability snapshot.
func (s *Server) SetCloudLLMProvider(p llm.Provider) { s.cloudLLMProvider = p }

// SetLocalLLMProvider attaches the native-tool-calling local provider (Ollama).
func (s *Server) SetLocalLLMProvider(p llm.Provider) { s.localLLMProvider = p }

// resolveMainProvider picks the llm.Provider for the main tool-loop per the
// active Locus Mode. Returns the provider, whether it's the cloud tier, whether
// this is a fallback (preferred tier unavailable), or an error when the mode
// forbids crossing and the required tier has no provider wired.
func (s *Server) resolveMainProvider() (llm.Provider, bool, bool, error) {
	mode, _ := locus.ParseMode(s.currentConfig.LocusMode)
	res := mode.Main()

	provForTier := func(t locus.Tier) llm.Provider {
		if t == locus.TierCloud {
			return s.cloudLLMProvider
		}
		return s.localLLMProvider
	}

	if p := provForTier(res.Preferred); p != nil {
		return p, res.Preferred == locus.TierCloud, false, nil
	}
	if res.CrossAllowed {
		if p := provForTier(res.Fallback); p != nil {
			return p, res.Fallback == locus.TierCloud, true, nil
		}
	}
	return nil, false, false, fmt.Errorf(
		"locus mode %q: no %s provider available (and fallback not permitted)",
		mode, res.Preferred)
}

// SetRuntimeManager attaches the local runtime/dashboard state manager.
func (s *Server) SetRuntimeManager(m localruntime.Manager) {
	s.runtimeManager = m
	s.refreshRuntimeEndpoints()
}

// NewServer creates a new Agent gRPC server.
func NewServer(a *agent.Agent, localProvider *legacymodels.LocalModelProvider, router RouterCloudUpdater, coordinator *loop.ADKCoordinator, cloudFactory agent.CloudFactory, registry *engine.EngineRegistry) *Server {
	return &Server{
		agent:         a,
		localProvider: localProvider,
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
	s.currentConfig = cfg
	s.refreshRuntimeEndpoints()
}

// UpdateConfig implements proto.AgentServer — updates runtime config without restart.
func (s *Server) UpdateConfig(ctx context.Context, req *proto.UpdateConfigRequest) (*proto.UpdateConfigResponse, error) {
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

	if req.LocalModel != "" {
		s.localProvider.SetModelName(req.LocalModel)
		changes = append(changes, fmt.Sprintf("local_model=%s", req.LocalModel))
		fmt.Printf("UpdateConfig: Local model set to %s\n", req.LocalModel)
	}

	if req.LocalRuntime != "" {
		if req.LocalRuntime != "ollama" && req.LocalRuntime != "llama_server" {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("invalid local_runtime %q: expected ollama or llama_server", req.LocalRuntime),
			}, nil
		}
		if s.registry == nil {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: "engine registry is not configured",
			}, nil
		}
		eng, err := s.registry.GetEngine(req.LocalRuntime)
		if err != nil {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("local runtime %q is not available: %v", req.LocalRuntime, err),
			}, nil
		}
		model := req.LocalModel
		if model == "" && req.LocalRuntime == "llama_server" {
			model = s.currentConfig.LlamaServer.DefaultModel
		}
		if model == "" {
			model = s.currentConfig.LocalModel
		}
		s.localProvider.SetEngine(eng, model)
		changes = append(changes, fmt.Sprintf("local_runtime=%s", req.LocalRuntime))
		fmt.Printf("UpdateConfig: Local runtime set to %s\n", req.LocalRuntime)
	}

	if req.LocusMode != "" {
		if _, err := locus.ParseMode(req.LocusMode); err != nil {
			return &proto.UpdateConfigResponse{Success: false, Message: err.Error()}, nil
		}
		changes = append(changes, fmt.Sprintf("locus_mode=%s", req.LocusMode))
		fmt.Printf("UpdateConfig: Locus mode set to %s\n", req.LocusMode)
	}

	// Cloud provider rebuild: any of provider / model / api_key / base_url
	// changes is enough to want a rebuild. We require provider to be set
	// (existing or new) and at least one of api_key / base_url so we don't
	// silently land back on the absent sentinel.
	wantCloudRebuild := req.CloudProvider != "" || req.CloudModel != "" || req.CloudApiKey != "" || req.CloudBaseUrl != ""
	if wantCloudRebuild {
		provider := req.CloudProvider
		if provider == "" {
			provider = s.currentConfig.CloudProvider
		}
		model := req.CloudModel
		if model == "" {
			model = s.currentConfig.CloudModel
		}
		apiKey := req.CloudApiKey
		if apiKey == "" {
			apiKey = s.currentConfig.CloudAPIKey
		}
		baseURL := req.CloudBaseUrl
		if baseURL == "" {
			baseURL = s.currentConfig.CloudBaseURL
		}
		if provider == "" || (apiKey == "" && baseURL == "") {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: "cloud config incomplete: need cloud_provider and at least one of cloud_api_key / cloud_base_url",
			}, nil
		}
		cp, err := s.cloudFactory(ctx, provider, model, apiKey, baseURL)
		if err != nil {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("failed to create cloud provider: %v", err),
			}, nil
		}
		s.router.SetCloudProvider(cp)
		s.coordinator.SetCloudProvider(cp)
		summary := fmt.Sprintf("%s/%s", provider, model)
		if baseURL != "" {
			summary += " @ " + baseURL
		}
		changes = append(changes, "cloud="+summary)
		fmt.Printf("UpdateConfig: Cloud provider set to %s\n", summary)
	}

	if len(changes) == 0 {
		return &proto.UpdateConfigResponse{
			Success: true,
			Message: "no changes requested",
		}, nil
	}

	if req.OllamaUrl != "" {
		s.currentConfig.OllamaURL = req.OllamaUrl
	}
	if req.LocalModel != "" {
		s.currentConfig.LocalModel = req.LocalModel
	}
	if req.LocalRuntime != "" {
		s.currentConfig.LocalRuntime = req.LocalRuntime
	}
	if req.CloudProvider != "" {
		s.currentConfig.CloudProvider = req.CloudProvider
	}
	if req.CloudModel != "" {
		s.currentConfig.CloudModel = req.CloudModel
	}
	if req.CloudApiKey != "" {
		s.currentConfig.CloudAPIKey = req.CloudApiKey
	}
	if req.CloudBaseUrl != "" {
		s.currentConfig.CloudBaseURL = req.CloudBaseUrl
	}
	if req.LocusMode != "" {
		s.currentConfig.LocusMode = req.LocusMode
	}
	s.refreshRuntimeEndpoints()

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
func estimateRawTokens(turns []conversation.Turn) int {
	n := 0
	for _, t := range turns {
		n += len(t.Content) + len(t.BlocksJSON)
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
			if state.ConsolidatedJSON == "" {
				sent = raw // no compaction → sent is the full history
			} else {
				view, _ := compactor.BuildSendView(turns, state)
				sent = compaction.TotalTokens(contextmeter.Default(), view)
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
	return &proto.GetConfigResponse{
		OllamaUrl:      s.currentConfig.OllamaURL,
		LocalModel:     s.currentConfig.LocalModel,
		EmbeddingModel: s.currentConfig.EmbeddingModel,
		CloudProvider:  s.currentConfig.CloudProvider,
		CloudModel:     s.currentConfig.CloudModel,
		CloudBaseUrl:   s.currentConfig.CloudBaseURL,
		CloudApiKeySet: s.currentConfig.CloudAPIKey != "",
		CloudState:     state,
		Port:           s.currentConfig.Port,
		LocalRuntime:   s.currentConfig.LocalRuntime,
		LocusMode:      s.currentConfig.LocusMode,
	}, nil
}

// ListModels implements proto.AgentServer — returns available models from the active local runtime.
func (s *Server) ListModels(ctx context.Context, req *proto.ListModelsRequest) (*proto.ListModelsResponse, error) {
	if s.registry == nil {
		return nil, fmt.Errorf("registry not configured")
	}
	runtimeName := s.currentConfig.LocalRuntime
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
func (s *Server) ListRuntimeModels(ctx context.Context, req *proto.ListRuntimeModelsRequest) (*proto.ListRuntimeModelsResponse, error) {
	if s.runtimeManager == nil {
		return &proto.ListRuntimeModelsResponse{}, nil
	}
	models, err := s.runtimeManager.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	return &proto.ListRuntimeModelsResponse{Models: mapRuntimeModels(models)}, nil
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
func (s *Server) DownloadRuntimeModel(ctx context.Context, req *proto.DownloadRuntimeModelRequest) (*proto.DownloadRuntimeModelResponse, error) {
	if s.runtimeManager == nil {
		return &proto.DownloadRuntimeModelResponse{Ok: false, Error: "runtime manager not configured"}, nil
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

func (s *Server) refreshRuntimeEndpoints() {
	if s.runtimeManager == nil {
		return
	}
	s.runtimeManager.SetEndpoints(localruntime.EndpointsFromConfig(s.currentConfig))
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

	if (s.cloudLLMProvider != nil || s.localLLMProvider != nil) && s.toolRegistry != nil {
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
func buildToolLoopSystem(env loopEnv, dirSnapshot, projectContext string) string {
	var b strings.Builder
	b.WriteString("You are Cercano, an agentic coding assistant operating in a terminal.\n\n")
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
	return buildToolLoopSystem(env, directorySnapshot(workDir, 80), s.loadProjectContext(workDir))
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
		}
	}

	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission) (bool, error) {
		if s.pendingDecisions == nil {
			return false, nil
		}
		if err := stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_PermissionRequired{
				PermissionRequired: &proto.PermissionRequired{
					ToolUseId: toolUseID,
					ToolName:  name,
					ArgsJson:  string(args),
					Tier:      string(tier),
				},
			},
		}); err != nil {
			return false, err
		}
		return s.pendingDecisions.Wait(ctx, toolUseID)
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
	injectedLen := len(convHistory)

	onTextDelta := func(t string) {
		stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_TokenDelta{
				TokenDelta: &proto.TokenDelta{Content: t},
			},
		})
	}

	result, loopErr := s.runMainLoop(ctx, req, provider, isCloud, sink, requester, convHistory, onTextDelta)
	if loopErr != nil {
		mode, _ := locus.ParseMode(s.currentConfig.LocusMode)
		res := mode.Main()
		fbProv := s.cloudLLMProvider
		fbCloud := true
		if res.Fallback == locus.TierLocal {
			fbProv, fbCloud = s.localLLMProvider, false
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
			result, loopErr = s.runMainLoop(ctx, req, fbProv, fbCloud, sink, requester, convHistory, onTextDelta)
		}
		if loopErr != nil {
			return fmt.Errorf("tool loop error: %w", loopErr)
		}
	}

	s.persistToolLoopTurns(ctx, req, result, injectedLen, s.mainModelFor(isCloud))
	s.agent.RecordContextUsage(req.GetConversationId(), s.mainModelFor(isCloud),
		result.InputTokens, result.OutputTokens)

	return stream.Send(&proto.StreamProcessResponse{
		Payload: &proto.StreamProcessResponse_FinalResponse{
			FinalResponse: &proto.ProcessRequestResponse{
				Output: strings.ToValidUTF8(result.FinalText, "�"),
				RoutingMetadata: &proto.RoutingMetadata{
					ModelName: provider.Name(),
				},
			},
		},
	})
}

// mainModelFor returns the configured model name for the active tier.
func (s *Server) mainModelFor(isCloud bool) string {
	if isCloud {
		return s.currentConfig.CloudModel
	}
	return s.currentConfig.LocalModel
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
	requester func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission) (bool, error),
	convHistory []llm.Message,
	onTextDelta func(string),
) (agent.ToolLoopResult, error) {
	return agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:            provider,
		Registry:            s.toolRegistry,
		Permissions:         s.permStore,
		UserInput:           req.GetInput(),
		Model:               s.mainModelFor(isCloud),
		System:              s.buildSystemPrompt(req.GetWorkDir()),
		EventSink:           sink,
		PermissionRequester: requester,
		ConvHistory:         convHistory,
		OnTextDelta:         onTextDelta,
	})
}

// persistToolLoopTurns persists the messages added this turn into the
// persistent conversation store. It writes result.History[injectedLen:] —
// every role, with BlocksJSON and concatenated text Content — so that user
// tool_result messages are saved alongside assistant turns. Best-effort:
// store errors are logged but never surfaced to the caller.
func (s *Server) persistToolLoopTurns(ctx context.Context, req *proto.ProcessRequestRequest, result agent.ToolLoopResult, injectedLen int, model string) {
	if s.agent == nil {
		return
	}
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return
	}
	if err := store.EnsureConversation(ctx, convID, req.GetWorkDir(), model); err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] EnsureConversation(%s) failed: %v\n", convID, err)
		return
	}
	// Persist only the messages added this turn — result.History begins with the
	// injected ConvHistory prefix, which is already stored. Clamp defensively.
	if injectedLen < 0 || injectedLen > len(result.History) {
		injectedLen = 0
	}
	for _, m := range result.History[injectedLen:] {
		blocksJSON, err := json.Marshal(m.Blocks)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[tool-loop] marshal blocks failed: %v\n", err)
			continue
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
	// Refresh the living recap for this conversation. The legacy path schedules
	// this inside the agent, but the tool-loop path persists turns here, so it
	// must trigger the recap too — otherwise recaps never update in the native
	// tool-calling (cloud) flow the CLI uses.
	s.agent.ScheduleRecap(convID)
	s.agent.ScheduleCompaction(convID)
}

// assembleHistory builds the conversation history to send: the compacted view
// (consolidated summary + live tail) when compaction state exists, else the full
// history. If the assembled history exceeds the hard-override fraction of the
// model's max context, it compacts synchronously once and reassembles.
func (s *Server) assembleHistory(ctx context.Context, store conversation.Store, convID string) []llm.Message {
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] GetTurns(%s) failed: %v\n", convID, err)
		return nil
	}
	state, _ := store.GetCompaction(ctx, convID)
	view, _ := compactor.BuildSendView(turns, state)

	pct := s.currentConfig.Compaction.HardOverridePct
	if s.currentConfig.Compaction.Enabled && pct > 0 {
		hardLimit := int(float64(contextmeter.ModelMax(s.currentConfig.CloudModel)) * pct)
		if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
			if err := s.agent.CompactNow(ctx, convID); err == nil {
				state, _ = store.GetCompaction(ctx, convID)
				view, _ = compactor.BuildSendView(turns, state)
			}
		}
	}
	return view
}

func (s *Server) mapRequest(req *proto.ProcessRequestRequest) *agent.Request {
	return &agent.Request{
		Input:          req.Input,
		WorkDir:        req.WorkDir,
		FileName:       req.FileName,
		ConversationID: req.ConversationId,
		DirectLocal:    req.DirectLocal,
		ModelOverride:  req.ModelOverride,
		Coproc:         req.Coproc,
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
	ok := s.pendingDecisions.Resolve(req.GetToolUseId(), true)
	return &proto.AllowToolCallResponse{Ok: ok}, nil
}

// DenyToolCall implements proto.AgentServer.
func (s *Server) DenyToolCall(ctx context.Context, req *proto.DenyToolCallRequest) (*proto.DenyToolCallResponse, error) {
	if s.pendingDecisions == nil {
		return &proto.DenyToolCallResponse{Ok: false}, nil
	}
	ok := s.pendingDecisions.Resolve(req.GetToolUseId(), false)
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
