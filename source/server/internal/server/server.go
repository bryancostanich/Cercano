package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/config"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
	"cercano/source/server/pkg/proto"
)

// Server is the gRPC server for the Agent service.
type Server struct {
	proto.UnimplementedAgentServer
	agent               *agent.Agent
	localProvider       *llm.LocalModelProvider
	router              *agent.SmartRouter
	coordinator         *loop.ADKCoordinator
	cloudFactory        agent.CloudFactory
	registry            *engine.EngineRegistry
	healthMonitorCancel context.CancelFunc // cancel function for the active health monitor
	configPath          string             // path to config.yaml for persistence
	currentConfig       config.Config      // current config state for persistence
}

// NewServer creates a new Agent gRPC server.
func NewServer(a *agent.Agent, localProvider *llm.LocalModelProvider, router *agent.SmartRouter, coordinator *loop.ADKCoordinator, cloudFactory agent.CloudFactory, registry *engine.EngineRegistry) *Server {
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

	if req.CloudApiKey != "" && req.CloudProvider != "" {
		model := req.CloudModel
		if model == "" {
			model = "gemini-3-flash" // sensible default
		}

		provider, err := s.cloudFactory(ctx, req.CloudProvider, model, req.CloudApiKey)
		if err != nil {
			return &proto.UpdateConfigResponse{
				Success: false,
				Message: fmt.Sprintf("failed to create cloud provider: %v", err),
			}, nil
		}

		s.router.SetCloudProvider(provider)
		s.coordinator.SetCloudProvider(provider)
		changes = append(changes, fmt.Sprintf("cloud_provider=%s/%s", req.CloudProvider, model))
		fmt.Printf("UpdateConfig: Cloud provider set to %s/%s\n", req.CloudProvider, model)
	}

	if len(changes) == 0 {
		return &proto.UpdateConfigResponse{
			Success: true,
			Message: "no changes requested",
		}, nil
	}

	// Persist changes to disk
	if s.configPath != "" {
		if req.OllamaUrl != "" {
			s.currentConfig.OllamaURL = req.OllamaUrl
		}
		if req.LocalModel != "" {
			s.currentConfig.LocalModel = req.LocalModel
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
		if err := config.Save(s.currentConfig, s.configPath); err != nil {
			fmt.Printf("UpdateConfig: warning — failed to persist config: %v\n", err)
		}
	}

	return &proto.UpdateConfigResponse{
		Success: true,
		Message: fmt.Sprintf("updated: [%s]", strings.Join(changes, ", ")),
	}, nil
}

// ListModels implements proto.AgentServer — returns available models from the active Ollama instance.
func (s *Server) ListModels(ctx context.Context, req *proto.ListModelsRequest) (*proto.ListModelsResponse, error) {
	if s.registry == nil {
		return nil, fmt.Errorf("registry not configured")
	}
	eng, err := s.registry.GetEngine("ollama")
	if err != nil {
		return nil, fmt.Errorf("failed to get ollama engine: %v", err)
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

	agentReq := s.mapRequest(req)

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

// MapResponseForTest exposes mapResponse for testing.
func (s *Server) MapResponseForTest(response *agent.Response) *proto.ProcessRequestResponse {
	return s.mapResponse(response)
}

func (s *Server) mapResponse(response *agent.Response) *proto.ProcessRequestResponse {
	// Sanitize output to valid UTF-8 — gRPC requires all string fields
	// to be valid UTF-8 and will fail marshaling otherwise.
	protoRes := &proto.ProcessRequestResponse{
		Output: strings.ToValidUTF8(response.Output, "\uFFFD"),
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
