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

	"google.golang.org/adk/session"
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

	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	embeddingModel := "nomic-embed-text"
	localModel := "qwen3-coder"

	// Pre-flight check for Ollama
	if err := checkOllama(context.Background(), ollamaURL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Initialize Providers
	localProvider := llm.NewOllamaProvider(localModel, ollamaURL)
	
	// Default to Mock for cloud, but upgrade if keys are present
	var cloudProvider agent.ModelProvider = llm.NewMockProvider("CloudModel")
	
	// Check for Cloud Keys
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey != "" {
		fmt.Println("Main: Detected GEMINI_API_KEY. Initializing real Cloud Provider (Google)...")
		cp, err := llm.NewCloudModelProvider(context.Background(), "google", "gemini-1.5-flash", geminiKey)
		if err == nil {
			cloudProvider = cp
		} else {
			fmt.Printf("Main: Failed to init real Cloud Provider: %v\n", err)
		}
	}

	validator := tools.NewGoValidator()
	sessionSvc := session.InMemoryService()
	coordinator := loop.NewADKCoordinator(localProvider, cloudProvider, validator, sessionSvc)

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

	convStore := agent.NewConversationStore(sessionSvc, 3)
	orchestrator := agent.NewAgent(smartRouter, coordinator, agent.WithConversationStore(convStore))

	port := os.Getenv("CERCANO_PORT")
	if port == "" {
		port = "50052"
	}
	lis, err := net.Listen("tcp", ":"+port)
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
