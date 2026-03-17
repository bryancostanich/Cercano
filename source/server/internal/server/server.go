package server

import (
	"context"
	"fmt"
	"net/url"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
	"cercano/source/server/pkg/proto"
)

// Server is the gRPC server for the Agent service.
type Server struct {
	proto.UnimplementedAgentServer
	agent         *agent.Agent
	localProvider *llm.OllamaProvider
	router        *agent.SmartRouter
	coordinator   *loop.ADKCoordinator
	cloudFactory  agent.CloudFactory
}

// NewServer creates a new Agent gRPC server.
func NewServer(a *agent.Agent, localProvider *llm.OllamaProvider, router *agent.SmartRouter, coordinator *loop.ADKCoordinator, cloudFactory agent.CloudFactory) *Server {
	return &Server{
		agent:         a,
		localProvider: localProvider,
		router:        router,
		coordinator:   coordinator,
		cloudFactory:  cloudFactory,
	}
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
		s.localProvider.SetBaseURL(req.OllamaUrl)
		changes = append(changes, fmt.Sprintf("ollama_url=%s", req.OllamaUrl))
		fmt.Printf("UpdateConfig: Ollama URL set to %s\n", req.OllamaUrl)
	}

	if req.LocalModel != "" {
		s.localProvider.SetModelName(req.LocalModel)
		changes = append(changes, fmt.Sprintf("local_model=%s", req.LocalModel))
		fmt.Printf("UpdateConfig: Local model set to %s\n", req.LocalModel)
	}

	if req.CloudApiKey != "" && req.CloudProvider != "" {
		model := req.CloudModel
		if model == "" {
			model = "gemini-1.5-flash" // sensible default
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

	return &proto.UpdateConfigResponse{
		Success: true,
		Message: fmt.Sprintf("updated: %v", changes),
	}, nil
}

// ProcessRequest implements proto.AgentServer (Unary).
func (s *Server) ProcessRequest(ctx context.Context, req *proto.ProcessRequestRequest) (*proto.ProcessRequestResponse, error) {
	fmt.Printf("Received request (Unary): %s\n", req.Input)

	agentReq := s.mapRequest(req)
	response, err := s.agent.ProcessRequest(ctx, agentReq)
	if err != nil {
		return nil, fmt.Errorf("agent error: %w", err)
	}

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
	}
}

func (s *Server) mapResponse(response *agent.Response) *proto.ProcessRequestResponse {
	protoRes := &proto.ProcessRequestResponse{
		Output: response.Output,
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

	protoRes.RoutingMetadata = &proto.RoutingMetadata{
		ModelName:  response.RoutingMetadata.ModelName,
		Confidence: float32(response.RoutingMetadata.Confidence),
		Escalated:  response.RoutingMetadata.Escalated,
	}

	protoRes.ValidationErrors = response.ValidationErrors

	return protoRes
}
