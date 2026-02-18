package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
	"cercano/source/server/internal/server"
	"cercano/source/server/internal/tools"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
)

func checkOllama(ctx context.Context, baseURL string, models ...string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	
	// Check if Ollama is running
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("\n\n[ERROR] Could not connect to Ollama at %s.\nIs Ollama running? Please start Ollama before running the Cercano agent.\nDownload it at https://ollama.com/", baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama returned an unexpected status: %d", resp.StatusCode)
	}

	// Basic check for required models could be added here if needed, 
	// but SmartRouter will also report missing models during initialization.
	return nil
}

func main() {
	fmt.Println("Starting Cercano AI Agent gRPC server...")

	ollamaURL := "http://localhost:11434"
	embeddingModel := "nomic-embed-text"
	localModel := "qwen3-coder"

	// Pre-flight check for Ollama
	if err := checkOllama(context.Background(), ollamaURL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Initialize Providers
	localProvider := llm.NewOllamaProvider(localModel, ollamaURL)
	cloudProvider := llm.NewMockProvider("CloudModel")

	localHandler := tools.NewGenericGenerator(localProvider)
	// TODO: Create a real CloudHandler when cloud integration is done.
	// For now, we reuse the localHandler or pass the mock.
	cloudHandler := tools.NewGenericGenerator(cloudProvider) 

	validator := tools.NewGoValidator()
	coordinator := loop.NewGenerationCoordinator(localHandler, cloudHandler, validator)

	smartRouter, err := agent.NewSmartRouter(localProvider, cloudProvider, embeddingModel, http.DefaultClient, "internal/agent/prototypes.yaml", func(ctx context.Context, provider, model, apiKey string) (agent.ModelProvider, error) {
		return llm.NewCloudModelProvider(ctx, provider, model, apiKey)
	})
	if err != nil {
		// If it's a connection error or missing model, format it nicely
		errMsg := err.Error()
		if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "no such host") {
			fmt.Fprintf(os.Stderr, "\n[ERROR] SmartRouter initialization failed: Could not connect to Ollama. Please ensure it is running at %s\n", ollamaURL)
		} else if strings.Contains(errMsg, "not found") {
			fmt.Fprintf(os.Stderr, "\n[ERROR] SmartRouter initialization failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "\n[ERROR] Failed to create router: %v\n", err)
		}
		os.Exit(1)
	}

	orchestrator := agent.NewAgent(smartRouter, coordinator)

	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	proto.RegisterAgentServer(s, server.NewServer(orchestrator))

	fmt.Printf("Server listening at %v\n", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
