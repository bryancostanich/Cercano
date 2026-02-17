package main

import (
	"fmt"
	"log"
	"net"
	"net/http"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/router"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
)

func main() {
	fmt.Println("Starting Cercano AI Agent gRPC server...")

	// Initialize Providers
	// TODO: Make configuration loadable
	localProvider := llm.NewOllamaProvider("qwen3-coder", "http://localhost:11434")
	cloudProvider := llm.NewMockProvider("CloudModel")

	// Initialize Router
	// Note: Expects to be run from 'source' directory where internal/router/prototypes.yaml is accessible
	smartRouter, err := router.NewSmartRouter(localProvider, cloudProvider, "nomic-embed-text", http.DefaultClient, "internal/router/prototypes.yaml")
	if err != nil {
		log.Fatalf("failed to create router: %v", err)
	}

	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	proto.RegisterAgentServer(s, agent.NewServer(smartRouter))

	fmt.Printf("Server listening at %v\n", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
