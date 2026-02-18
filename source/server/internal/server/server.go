package server

import (
	"context"
	"fmt"

	"cercano/source/server/internal/agent"
	"cercano/source/server/pkg/proto" // Import the generated protobuf package
)

// Server is the gRPC server for the Agent service.
type Server struct {
	proto.UnimplementedAgentServer
	agent *agent.Agent
}

// NewServer creates a new Agent gRPC server.
func NewServer(a *agent.Agent) *Server {
	return &Server{agent: a}
}

// ProcessRequest implements proto.AgentServer.
func (s *Server) ProcessRequest(ctx context.Context, req *proto.ProcessRequestRequest) (*proto.ProcessRequestResponse, error) {
	fmt.Printf("Received request: %s\n", req.Input)

	agentReq := &agent.Request{
		Input:    req.Input,
		WorkDir:  req.WorkDir,
		FileName: req.FileName,
	}
	if req.ProviderConfig != nil {
		agentReq.ProviderConfig = &agent.ProviderConfig{
			Provider: req.ProviderConfig.Provider,
			Model:    req.ProviderConfig.Model,
			ApiKey:   req.ProviderConfig.ApiKey,
		}
	}

	response, err := s.agent.ProcessRequest(ctx, agentReq)
	if err != nil {
		return nil, fmt.Errorf("agent error: %w", err)
	}

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

	return protoRes, nil
}