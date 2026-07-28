package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/engine"
	llamaengine "cercano/source/server/internal/engine/llamaserver"
	mistralengine "cercano/source/server/internal/engine/mistralrs"
	"cercano/source/server/internal/engine/ollama"
	"cercano/source/server/internal/inference"
	ollamallm "cercano/source/server/internal/llm/ollama"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/localruntime/catalogdefaults"
	runtimellama "cercano/source/server/internal/localruntime/llamaserver"
	runtimemistralrs "cercano/source/server/internal/localruntime/mistralrs"
	"cercano/source/server/internal/loop"
	"cercano/source/server/internal/openmodels"
	"cercano/source/server/internal/server"
	"cercano/source/server/internal/sysram"
	"cercano/source/server/internal/tools"
	"cercano/source/server/pkg/config"
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

	fmt.Printf("Local model: %s\n", openChatModel(cfg))
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
	mistralEng := mistralengine.NewEngine(runtimeManager)
	registry.RegisterEngine(mistralEng)

	// Initialize Providers
	openProviderFor := func(c config.Config) inference.Provider {
		if strings.EqualFold(c.OpenRuntime, "llama_server") {
			return llamaengine.NewLLMProvider(llamaEng)
		}
		if strings.EqualFold(c.OpenRuntime, "mistralrs") {
			return mistralengine.NewLLMProvider(mistralEng)
		}
		return ollamallm.NewClient(ollamallm.Config{
			BaseURL: c.OllamaURL,
			Model:   openChatModel(c),
		})
	}
	openProvider := agent.InferenceTurnRunner(openProviderFor(cfg), openTurnModel(cfg))

	// Cloud provider construction: only build a real one when there's enough
	// config to actually reach a cloud (API key OR a proxy baseURL). Otherwise
	// use a sentinel that auto-degrades to local at turn time with a notice.
	var cloudProvider agent.TurnRunner
	if cfg.CloudProvider != "" && (cfg.CloudAPIKey != "" || cfg.CloudBaseURL != "") {
		fmt.Printf("Main: Initializing Cloud Provider (%s)...\n", cfg.CloudProvider)
		prof := cloudfactory.LegacyProfile(cfg.CloudProvider, cfg.CloudModel, cfg.CloudBaseURL)
		p, err := cloudfactory.BuildCloudProvider(prof, cfg.CloudAPIKey)
		if err == nil {
			cloudProvider = agent.InferenceTurnRunner(p, cfg.CloudModel)
		} else {
			fmt.Printf("Main: Failed to init Cloud Provider: %v — degrading to local-only.\n", err)
			cloudProvider = agent.AbsentCloud("provider init failed: " + err.Error())
		}
	} else {
		reason := "no API key or base URL configured"
		if cfg.CloudProvider == "" {
			reason = "no provider selected"
		}
		cloudProvider = agent.AbsentCloud(reason)
	}

	validator := tools.NewAutoValidator(tools.DefaultLoader(), tools.DefaultKindToValidator())
	sessionSvc := session.InMemoryService()
	coordinator := loop.NewADKCoordinator(openProvider, cloudProvider, validator, sessionSvc)

	smartRouter, err := agent.NewSmartRouterFromBytes(openProvider, cloudProvider, openEmbeddingModel(cfg), ollamaEng, agent.DefaultPrototypes(), func(ctx context.Context, provider, model, apiKey, baseURL string) (agent.TurnRunner, error) {
		prof := cloudfactory.LegacyProfile(provider, model, baseURL)
		p, err := cloudfactory.BuildCloudProvider(prof, apiKey)
		if err != nil {
			return nil, err
		}
		return agent.InferenceTurnRunner(p, model), nil
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

	cloudFactory := func(ctx context.Context, provider, model, apiKey, baseURL string) (agent.TurnRunner, error) {
		prof := cloudfactory.LegacyProfile(provider, model, baseURL)
		p, err := cloudfactory.BuildCloudProvider(prof, apiKey)
		if err != nil {
			return nil, err
		}
		return agent.InferenceTurnRunner(p, model), nil
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
	srv := server.NewServer(orchestrator, smartRouter, coordinator, cloudFactory, registry)
	srv.SetRuntimeManager(runtimeManager)
	srv.SetConfigPersistence(config.DefaultPath(), cfg)
	proto.RegisterAgentServer(s, srv)

	fmt.Printf("Server listening at %v\n", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func buildRuntimeManager(cfg config.Config) localruntime.Manager {
	manager := localruntime.NewManager(localruntime.WithEndpoints(localruntime.EndpointsFromConfig(cfg, openChatModel(cfg), openEmbeddingModel(cfg))))
	sweepStalePartials(cfg, manager)
	provider := runtimellama.NewProvider(cfg.LlamaServer)
	// Reap llama-servers orphaned by cercano processes that died without
	// cleanup — before the enabled check, because orphans from when the
	// runtime WAS enabled hold GPU memory regardless of current config.
	provider.SweepOrphans(manager)
	manager.RegisterProvider(provider)

	mistralProvider := runtimemistralrs.NewProvider(cfg.MistralRS)
	mistralProvider.SweepOrphans(manager)
	manager.RegisterProvider(mistralProvider)
	manager.WriteLog(localruntime.LogEntry{
		Source:  "cercano.runtime.mistralrs",
		Level:   "info",
		Message: "mistral.rs provider registered",
	})

	runtime, model := activeRuntimeDefaultModel(cfg)
	if model == "" {
		manager.WriteLog(localruntime.LogEntry{
			Source:  "cercano.runtime." + runtime,
			Level:   "info",
			Message: runtime + " provider registered; no default_model configured",
		})
		return manager
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	if _, err := manager.Start(ctx, localruntime.StartRequest{Runtime: runtime, ModelID: model}); err != nil {
		manager.WriteLog(localruntime.LogEntry{
			Source:  "cercano.runtime." + runtime,
			Level:   "error",
			Message: "failed to start " + runtime + ": " + err.Error(),
		})
	}
	return manager
}

func activeRuntimeDefaultModel(cfg config.Config) (string, string) {
	if strings.EqualFold(cfg.OpenRuntime, "mistralrs") {
		return "mistralrs", strings.TrimSpace(cfg.MistralRS.DefaultModel)
	}
	if llamaServerEnabled(cfg) {
		return "llama_server", strings.TrimSpace(cfg.LlamaServer.DefaultModel)
	}
	return "ollama", ""
}

// sweepStalePartials removes .part files older than a week from the
// configured model directories — orphans from server kills mid-
// download. Recent partials are kept; they feed download resume.
func sweepStalePartials(cfg config.Config, manager localruntime.Manager) {
	for _, dir := range cfg.LlamaServer.ModelDirs {
		if strings.HasPrefix(dir, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(home, dir[2:])
			}
		}
		removed, err := localruntime.SweepStalePartials(dir, localruntime.DefaultPartialMaxAge)
		if err != nil {
			continue
		}
		for _, p := range removed {
			manager.WriteLog(localruntime.LogEntry{
				Source:  "cercano.runtime.download",
				Level:   "info",
				Message: "removed stale partial " + filepath.Base(p),
			})
		}
	}
}

func llamaServerEnabled(cfg config.Config) bool {
	return cfg.LlamaServer.Enabled || strings.EqualFold(cfg.OpenRuntime, "llama_server")
}

func openTierModel(cfg config.Config, tier config.Tier) string {
	return openmodels.EffectiveModel(cfg, tier, catalogdefaults.ForRuntime, uint64(sysram.Total()))
}

func openChatModel(cfg config.Config) string { return openTierModel(cfg, config.TierEveryday) }

func openEmbeddingModel(cfg config.Config) string { return openTierModel(cfg, config.TierEmbedding) }

func openTurnModel(cfg config.Config) string {
	if strings.EqualFold(cfg.OpenRuntime, "mistralrs") {
		model := strings.TrimSpace(cfg.MistralRS.DefaultModel)
		if model != "" {
			return model
		}
	}
	if strings.EqualFold(cfg.OpenRuntime, "llama_server") {
		model := strings.TrimSpace(cfg.LlamaServer.DefaultModel)
		if model != "" {
			return model
		}
	}
	return openChatModel(cfg)
}
