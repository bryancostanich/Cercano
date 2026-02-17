package agent

import (
	"context"
	"fmt"

	"cercano/source/internal/router"
	"cercano/source/proto" // Import the generated protobuf package
)

// Server is the gRPC server for the Agent service.
type Server struct {
	proto.UnimplementedAgentServer
	router router.Router
}

// NewServer creates a new Agent gRPC server.
func NewServer(r router.Router) *Server {
	return &Server{router: r}
}

// ProcessRequest implements proto.AgentServer.
func (s *Server) ProcessRequest(ctx context.Context, req *proto.ProcessRequestRequest) (*proto.ProcessRequestResponse, error) {
	fmt.Printf("Received request: %s\n", req.Input)

	routerReq := &router.Request{Input: req.Input}
	provider, err := s.router.SelectProvider(routerReq)
	if err != nil {
		return nil, fmt.Errorf("router error: %w", err)
	}

	response, err := provider.Process(ctx, routerReq)
	if err != nil {
		return nil, fmt.Errorf("provider processing error: %w", err)
	}

	return &proto.ProcessRequestResponse{
		Output: response.Output,
	}, nil
}

