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
	"cercano/source/server/internal/config"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/engine/ollama"
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

	return nil
}

func main() {
	const version = "0.3.0"
	fmt.Printf("Starting Cercano AI Agent gRPC server (v%s)...\n", version)

	// Load config: file → env vars → defaults
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to load config: %v (using defaults)\n", err)
		cfg = config.Defaults()
	}

	fmt.Printf("Local model: %s\n", cfg.LocalModel)
	fmt.Printf("Ollama URL: %s\n", cfg.OllamaURL)
	if cfg.CloudProvider != "" {
		fmt.Printf("Cloud provider: %s (%s)\n", cfg.CloudProvider, cfg.CloudModel)
	}

	// Pre-flight check for Ollama
	if err := checkOllama(context.Background(), cfg.OllamaURL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	registry := engine.NewEngineRegistry()
	ollamaEng := ollama.NewOllamaEngine(cfg.OllamaURL)
	registry.RegisterEngine(ollamaEng)
	registry.RegisterEmbedder(ollamaEng)

	// Initialize Providers
	localProvider := llm.NewLocalModelProvider(ollamaEng, cfg.LocalModel)

	// Default to Mock for cloud, but upgrade if keys are present
	var cloudProvider agent.ModelProvider = llm.NewMockProvider("CloudModel")

	if cfg.CloudAPIKey != "" && cfg.CloudProvider != "" {
		fmt.Printf("Main: Initializing Cloud Provider (%s)...\n", cfg.CloudProvider)
		cp, err := llm.NewCloudModelProvider(context.Background(), cfg.CloudProvider, cfg.CloudModel, cfg.CloudAPIKey)
		if err == nil {
			cloudProvider = cp
		} else {
			fmt.Printf("Main: Failed to init Cloud Provider: %v\n", err)
		}
	}

	validator := tools.NewGoValidator()
	sessionSvc := session.InMemoryService()
	coordinator := loop.NewADKCoordinator(localProvider, cloudProvider, validator, sessionSvc)

	smartRouter, err := agent.NewSmartRouter(localProvider, cloudProvider, cfg.EmbeddingModel, ollamaEng, "internal/agent/prototypes.yaml", func(ctx context.Context, provider, model, apiKey string) (agent.ModelProvider, error) {
		return llm.NewCloudModelProvider(ctx, provider, model, apiKey)
	})
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "no such host") {
			fmt.Fprintf(os.Stderr, "\n[ERROR] SmartRouter initialization failed: Could not connect to Ollama. Please ensure it is running at %s\n", cfg.OllamaURL)
		} else if strings.Contains(errMsg, "not found") {
			fmt.Fprintf(os.Stderr, "\n[ERROR] SmartRouter initialization failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "\n[ERROR] Failed to create router: %v\n", err)
		}
		os.Exit(1)
	}

	convStore := agent.NewConversationStore(sessionSvc, 3)
	orchestrator := agent.NewAgent(smartRouter, coordinator, agent.WithConversationStore(convStore))

	cloudFactory := func(ctx context.Context, provider, model, apiKey string) (agent.ModelProvider, error) {
		return llm.NewCloudModelProvider(ctx, provider, model, apiKey)
	}

	lis, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	proto.RegisterAgentServer(s, server.NewServer(orchestrator, localProvider, smartRouter, coordinator, cloudFactory, registry))

	fmt.Printf("Server listening at %v\n", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
