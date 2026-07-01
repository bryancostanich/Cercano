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
	"cercano/source/server/pkg/config"
	"cercano/source/server/internal/engine"
	llamaengine "cercano/source/server/internal/engine/llamaserver"
	"cercano/source/server/internal/engine/ollama"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/localruntime"
	runtimellama "cercano/source/server/internal/localruntime/llamaserver"
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

	runtimeManager := buildRuntimeManager(cfg)
	llamaEng := llamaengine.NewEngine(runtimeManager)
	registry.RegisterEngine(llamaEng)

	// Initialize Providers
	localEngine, localModel := selectLocalEngine(cfg, ollamaEng, llamaEng)
	localProvider := legacymodels.NewLocalModelProvider(localEngine, localModel)

	// Cloud provider construction: only build a real one when there's enough
	// config to actually reach a cloud (API key OR a proxy baseURL). Otherwise
	// use a sentinel that auto-degrades to local at turn time with a notice.
	var cloudProvider agent.ModelProvider
	if cfg.CloudProvider != "" && (cfg.CloudAPIKey != "" || cfg.CloudBaseURL != "") {
		fmt.Printf("Main: Initializing Cloud Provider (%s)...\n", cfg.CloudProvider)
		cp, err := legacymodels.NewCloudModelProvider(context.Background(), cfg.CloudProvider, cfg.CloudModel, cfg.CloudAPIKey, cfg.CloudBaseURL)
		if err == nil {
			cloudProvider = cp
		} else {
			fmt.Printf("Main: Failed to init Cloud Provider: %v — degrading to local-only.\n", err)
			cloudProvider = legacymodels.NewAbsentCloudProvider("provider init failed: " + err.Error())
		}
	} else {
		reason := "no API key or base URL configured"
		if cfg.CloudProvider == "" {
			reason = "no provider selected"
		}
		cloudProvider = legacymodels.NewAbsentCloudProvider(reason)
	}

	validator := tools.NewAutoValidator(tools.DefaultLoader(), tools.DefaultKindToValidator())
	sessionSvc := session.InMemoryService()
	coordinator := loop.NewADKCoordinator(localProvider, cloudProvider, validator, sessionSvc)

	smartRouter, err := agent.NewSmartRouterFromBytes(localProvider, cloudProvider, cfg.EmbeddingModel, ollamaEng, agent.DefaultPrototypes(), func(ctx context.Context, provider, model, apiKey, baseURL string) (agent.ModelProvider, error) {
		return legacymodels.NewCloudModelProvider(ctx, provider, model, apiKey, baseURL)
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

	cloudFactory := func(ctx context.Context, provider, model, apiKey, baseURL string) (agent.ModelProvider, error) {
		return legacymodels.NewCloudModelProvider(ctx, provider, model, apiKey, baseURL)
	}

	lis, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// 64 MiB comfortably fits multiple 20 MiB images (the per-image client cap).
	const maxGRPCMessageBytes = 64 << 20
	s := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxGRPCMessageBytes),
		// Recover handler panics so one bad RPC returns codes.Internal instead of
		// crashing the singleton agent and dropping every client's stream.
		grpc.ChainUnaryInterceptor(server.RecoveryUnaryInterceptor()),
		grpc.ChainStreamInterceptor(server.RecoveryStreamInterceptor()),
	)
	srv := server.NewServer(orchestrator, localProvider, smartRouter, coordinator, cloudFactory, registry)
	srv.SetRuntimeManager(runtimeManager)
	srv.SetConfigPersistence(config.DefaultPath(), cfg)
	proto.RegisterAgentServer(s, srv)

	fmt.Printf("Server listening at %v\n", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func buildRuntimeManager(cfg config.Config) localruntime.Manager {
	manager := localruntime.NewManager(localruntime.WithEndpoints(localruntime.EndpointsFromConfig(cfg)))
	if !llamaServerEnabled(cfg) {
		return manager
	}
	provider := runtimellama.NewProvider(cfg.LlamaServer)
	manager.RegisterProvider(provider)
	if strings.TrimSpace(cfg.LlamaServer.DefaultModel) == "" {
		manager.WriteLog(localruntime.LogEntry{
			Source:  "cercano.runtime.llama_server",
			Level:   "info",
			Message: "llama-server provider registered; no default_model configured",
		})
		return manager
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	if _, err := manager.Start(ctx, localruntime.StartRequest{
		Runtime: "llama_server",
		ModelID: cfg.LlamaServer.DefaultModel,
	}); err != nil {
		manager.WriteLog(localruntime.LogEntry{
			Source:  "cercano.runtime.llama_server",
			Level:   "error",
			Message: "failed to start llama-server: " + err.Error(),
		})
	}
	return manager
}

func llamaServerEnabled(cfg config.Config) bool {
	return cfg.LlamaServer.Enabled || strings.EqualFold(cfg.LocalRuntime, "llama_server")
}

func selectLocalEngine(cfg config.Config, ollamaEng engine.InferenceEngine, llamaEng engine.InferenceEngine) (engine.InferenceEngine, string) {
	if strings.EqualFold(cfg.LocalRuntime, "llama_server") {
		model := strings.TrimSpace(cfg.LlamaServer.DefaultModel)
		if model == "" {
			model = cfg.LocalModel
		}
		return llamaEng, model
	}
	return ollamaEng, cfg.LocalModel
}
