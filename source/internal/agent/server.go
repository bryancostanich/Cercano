package agent

import (
	"context"
	"fmt"

	"cercano/source/proto" // Import the generated protobuf package
)

// Server is the gRPC server for the Agent service.
type Server struct {
	proto.UnimplementedAgentServer
}

// NewServer creates a new Agent gRPC server.
func NewServer() *Server {
	return &Server{}
}

// ProcessRequest implements proto.AgentServer.
func (s *Server) ProcessRequest(ctx context.Context, req *proto.ProcessRequestRequest) (*proto.ProcessRequestResponse, error) {
	fmt.Printf("Received request: %s\n", req.Input)
	// Placeholder implementation
	return &proto.ProcessRequestResponse{
		Output: fmt.Sprintf("Processed: %s", req.Input),
	}, nil
}

