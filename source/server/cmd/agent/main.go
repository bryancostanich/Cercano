package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
	"cercano/source/server/internal/server"
	"cercano/source/server/internal/tools"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
)

func main() {
	fmt.Println("Starting Cercano AI Agent gRPC server...")

	// Initialize Providers
	// TODO: Make configuration loadable
	localProvider := llm.NewOllamaProvider("qwen3-coder", "http://localhost:11434")
	cloudProvider := llm.NewMockProvider("CloudModel")

	handler := tools.NewGenericGenerator(localProvider)
	validator := tools.NewGoTestValidator()
	coordinator := loop.NewGenerationCoordinator(handler, validator)
	_ = coordinator // Will be used by the Agent orchestrator soon

	// Initialize Agent (formerly Router)
	// Note: Expects to be run from 'source/server' directory where internal/agent/prototypes.yaml is accessible
	// I need to make sure the path is correct relative to execution.
	smartAgent, err := agent.NewSmartRouter(localProvider, cloudProvider, "nomic-embed-text", http.DefaultClient, "internal/agent/prototypes.yaml", func(ctx context.Context, provider, model, apiKey string) (agent.ModelProvider, error) {
		return llm.NewCloudModelProvider(ctx, provider, model, apiKey)
	})
	if err != nil {
		log.Fatalf("failed to create agent: %v", err)
	}

	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	proto.RegisterAgentServer(s, server.NewServer(smartAgent))

	fmt.Printf("Server listening at %v\n", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}