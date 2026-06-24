package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/anthropic"
	"cercano/source/server/internal/localruntime"
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
	runtimeManager      localruntime.Manager
}

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

// GetContextUsage implements proto.AgentServer — reports cumulative token
// usage vs. the active model's context-window size for a conversation.
func (s *Server) GetContextUsage(ctx context.Context, req *proto.GetContextUsageRequest) (*proto.GetContextUsageResponse, error) {
	used, max := s.agent.GetContextUsage(ctx, req.GetConversationId())
	var pct float64
	if max > 0 {
		pct = float64(used) / float64(max)
		if pct > 1 {
			pct = 1
		}
	}
	return &proto.GetContextUsageResponse{
		TokensUsed: int32(used),
		ModelMax:   int32(max),
		Percent:    pct,
	}, nil
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

	if s.cloudLLMProvider != nil && s.toolRegistry != nil {
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

	// The native tool-loop provider is the configured cloud (Anthropic) engine.
	// Announce the route up front so the client can show the engine badge from
	// the first frame.
	stream.Send(&proto.StreamProcessResponse{
		Payload: &proto.StreamProcessResponse_RouteSelected{
			RouteSelected: &proto.RouteSelected{
				Model:   s.currentConfig.CloudModel,
				IsCloud: true,
			},
		},
	})

	result, err := agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:            s.cloudLLMProvider,
		Registry:            s.toolRegistry,
		Permissions:         s.permStore,
		UserInput:           req.GetInput(),
		Model:               s.currentConfig.CloudModel,
		EventSink:           sink,
		PermissionRequester: requester,
	})
	if err != nil {
		return fmt.Errorf("tool loop error: %w", err)
	}

	s.persistToolLoopTurns(ctx, req, result, 0)

	return stream.Send(&proto.StreamProcessResponse{
		Payload: &proto.StreamProcessResponse_FinalResponse{
			FinalResponse: &proto.ProcessRequestResponse{
				Output: strings.ToValidUTF8(result.FinalText, "�"),
				RoutingMetadata: &proto.RoutingMetadata{
					ModelName: s.cloudLLMProvider.Name(),
				},
			},
		},
	})
}

// persistToolLoopTurns persists the messages added this turn into the
// persistent conversation store. It writes result.History[injectedLen:] —
// every role, with BlocksJSON and concatenated text Content — so that user
// tool_result messages are saved alongside assistant turns. Best-effort:
// store errors are logged but never surfaced to the caller.
func (s *Server) persistToolLoopTurns(ctx context.Context, req *proto.ProcessRequestRequest, result agent.ToolLoopResult, injectedLen int) {
	if s.agent == nil {
		return
	}
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return
	}
	if err := store.EnsureConversation(ctx, convID, req.GetWorkDir(), s.currentConfig.CloudModel); err != nil {
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
}

func (s *Server) mapRequest(req *proto.ProcessRequestRequest) *agent.Request {
	return &agent.Request{
		Input:          req.Input,
		WorkDir:        req.WorkDir,
		FileName:       req.FileName,
		ConversationID: req.ConversationId,
		DirectLocal:    req.DirectLocal,
		ModelOverride:  req.ModelOverride,
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
