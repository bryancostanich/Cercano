package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/config"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
	mcpserver "cercano/source/server/internal/mcp"
	"cercano/source/server/internal/server"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/tools"
	"cercano/source/server/pkg/proto"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// version is set at build time via -ldflags "-X main.version=...".
// Falls back to "dev" for local builds.
var version = "dev"

func checkOllama(baseURL string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("could not connect to Ollama at %s. Is Ollama running? Download it at https://ollama.com/", baseURL)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama returned unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// startGRPCServer initializes all providers and starts the gRPC server.
// Returns the listener address and a cleanup function.
func startGRPCServer(cfg config.Config, bindAddr string) (string, func(), error) {
	if err := checkOllama(cfg.OllamaURL); err != nil {
		return "", nil, err
	}

	localProvider := llm.NewOllamaProvider(cfg.LocalModel, cfg.OllamaURL)

	var cloudProvider agent.ModelProvider = llm.NewMockProvider("CloudModel")
	if cfg.CloudAPIKey != "" && cfg.CloudProvider != "" {
		fmt.Fprintf(os.Stderr, "Initializing Cloud Provider (%s)...\n", cfg.CloudProvider)
		cp, err := llm.NewCloudModelProvider(context.Background(), cfg.CloudProvider, cfg.CloudModel, cfg.CloudAPIKey)
		if err == nil {
			cloudProvider = cp
		} else {
			fmt.Fprintf(os.Stderr, "Failed to init Cloud Provider: %v\n", err)
		}
	}

	validator := tools.NewGoValidator()
	sessionSvc := session.InMemoryService()
	coordinator := loop.NewADKCoordinator(localProvider, cloudProvider, validator, sessionSvc)

	cloudFactory := func(ctx context.Context, provider, model, apiKey string) (agent.ModelProvider, error) {
		return llm.NewCloudModelProvider(ctx, provider, model, apiKey)
	}

	// Resolve prototypes.yaml relative to the binary location so the server
	// works regardless of the working directory (important for MCP stdio mode).
	exePath, _ := os.Executable()
	serverRoot := filepath.Dir(filepath.Dir(exePath)) // bin/cercano -> bin -> server root
	prototypesPath := filepath.Join(serverRoot, "internal", "agent", "prototypes.yaml")
	smartRouter, err := agent.NewSmartRouter(localProvider, cloudProvider, cfg.EmbeddingModel, http.DefaultClient, prototypesPath, cloudFactory)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "no such host") {
			return "", nil, fmt.Errorf("SmartRouter init failed: could not connect to Ollama at %s", cfg.OllamaURL)
		}
		return "", nil, fmt.Errorf("SmartRouter init failed: %v", err)
	}

	convStore := agent.NewConversationStore(sessionSvc, 3)
	orchestrator := agent.NewAgent(smartRouter, coordinator, agent.WithConversationStore(convStore))

	lis, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return "", nil, fmt.Errorf("failed to listen on %s: %v", bindAddr, err)
	}

	s := grpc.NewServer()
	proto.RegisterAgentServer(s, server.NewServer(orchestrator, localProvider, smartRouter, coordinator, cloudFactory))

	go func() {
		if err := s.Serve(lis); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	cleanup := func() {
		s.GracefulStop()
	}

	return lis.Addr().String(), cleanup, nil
}

func main() {
	// Handle subcommands before flag parsing
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			runSetup()
			return
		case "version":
			fmt.Printf("cercano v%s\n", version)
			return
		case "stats":
			runStats()
			return
		}
	}

	mcpMode := flag.Bool("mcp", false, "Run in MCP mode (embedded gRPC server + MCP on stdio)")
	grpcAddr := flag.String("grpc-addr", "", "Address of an external gRPC server (MCP-only, no embedded server)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	showStats := flag.Bool("stats", false, "Print usage statistics and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("cercano v%s\n", version)
		return
	}

	if *showStats {
		runStats()
		return
	}

	// Load config
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to load config: %v (using defaults)\n", err)
		cfg = config.Defaults()
	}

	if *mcpMode {
		runMCPMode(cfg, *grpcAddr)
	} else {
		runServerMode(cfg)
	}
}

// runSetup checks prerequisites and pulls required Ollama models.
func runSetup() {
	fmt.Printf("Cercano Setup (v%s)\n", version)
	fmt.Println("Checking prerequisites...")

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		cfg = config.Defaults()
	}

	// Check Ollama is running
	fmt.Printf("\n[1/3] Checking Ollama at %s...\n", cfg.OllamaURL)
	if err := checkOllama(cfg.OllamaURL); err != nil {
		fmt.Fprintf(os.Stderr, "  FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  OK: Ollama is running.")

	// Check required models
	fmt.Println("\n[2/3] Checking required models...")
	requiredModels := []string{cfg.LocalModel, cfg.EmbeddingModel}
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(cfg.OllamaURL + "/api/tags")
	if err != nil {
		fmt.Fprintf(os.Stderr, "  FAIL: Could not list models: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	type modelList struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	var models modelList
	if err := decodeJSON(resp.Body, &models); err != nil {
		fmt.Fprintf(os.Stderr, "  FAIL: Could not parse model list: %v\n", err)
		os.Exit(1)
	}

	installed := make(map[string]bool)
	for _, m := range models.Models {
		// Strip :latest suffix for comparison
		name := strings.TrimSuffix(m.Name, ":latest")
		installed[name] = true
		installed[m.Name] = true
	}

	allPresent := true
	for _, model := range requiredModels {
		if installed[model] {
			fmt.Printf("  OK: %s\n", model)
		} else {
			fmt.Printf("  MISSING: %s — pulling...\n", model)
			if err := pullModel(cfg.OllamaURL, model); err != nil {
				fmt.Fprintf(os.Stderr, "  FAIL: Could not pull %s: %v\n", model, err)
				allPresent = false
			} else {
				fmt.Printf("  OK: %s (pulled)\n", model)
			}
		}
	}

	if !allPresent {
		os.Exit(1)
	}

	// Check/create config file
	fmt.Println("\n[3/3] Checking config file...")
	configPath := config.DefaultPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("  Creating default config at %s\n", configPath)
		if err := config.Save(cfg, configPath); err != nil {
			fmt.Fprintf(os.Stderr, "  WARN: Could not create config file: %v\n", err)
		} else {
			fmt.Println("  OK: Config file created.")
		}
	} else {
		fmt.Printf("  OK: Config file exists at %s\n", configPath)
	}

	fmt.Println("\nSetup complete! Run 'cercano' to start the server.")
}

func decodeJSON(r io.Reader, v interface{}) error {
	return json.NewDecoder(r).Decode(v)
}

func pullModel(ollamaURL, model string) error {
	payload := fmt.Sprintf(`{"name":"%s"}`, model)
	resp, err := http.Post(ollamaURL+"/api/pull", "application/json", strings.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama returned status %d", resp.StatusCode)
	}
	// Read through the streaming response to completion
	buf := make([]byte, 4096)
	for {
		_, err := resp.Body.Read(buf)
		if err != nil {
			break
		}
	}
	return nil
}

// runStats prints cumulative usage statistics and exits.
func runStats() {
	telemetryPath := filepath.Join(filepath.Dir(config.DefaultPath()), "telemetry.db")
	store, err := telemetry.NewSQLiteStore(telemetryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open telemetry database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	stats, err := store.GetStats(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to query stats: %v\n", err)
		os.Exit(1)
	}

	totalLocal := stats.TotalInputTokens + stats.TotalOutputTokens
	totalCloud := stats.TotalCloudInputTokens + stats.TotalCloudOutputTokens

	fmt.Printf("Cercano Usage Statistics (v%s)\n\n", version)
	fmt.Printf("  Total requests:          %d\n", stats.TotalRequests)
	fmt.Printf("  Local tokens processed:  %d (%d in, %d out)\n", totalLocal, stats.TotalInputTokens, stats.TotalOutputTokens)
	if totalCloud > 0 {
		fmt.Printf("  Cloud tokens (reported): %d (%d in, %d out)\n", totalCloud, stats.TotalCloudInputTokens, stats.TotalCloudOutputTokens)
		fmt.Printf("  Kept local:              %.1f%%\n", stats.LocalPercentage)
	} else {
		fmt.Printf("  Est. cloud tokens saved: %d\n", stats.LocalTokensSaved)
	}

	if len(stats.ByTool) > 0 {
		fmt.Printf("\n  By Tool:\n")
		for _, t := range stats.ByTool {
			fmt.Printf("    %-25s %d calls, %d tokens\n", t.Name, t.Count, t.InputTokens+t.OutputTokens)
		}
	}

	if len(stats.ByModel) > 0 {
		fmt.Printf("\n  By Model:\n")
		for _, m := range stats.ByModel {
			fmt.Printf("    %-25s %d calls, %d tokens\n", m.Name, m.Count, m.InputTokens+m.OutputTokens)
		}
	}

	if len(stats.ByDay) > 0 {
		fmt.Printf("\n  Recent Activity:\n")
		limit := len(stats.ByDay)
		if limit > 7 {
			limit = 7
		}
		for _, d := range stats.ByDay[:limit] {
			fmt.Printf("    %-25s %d calls, %d tokens\n", d.Name, d.Count, d.InputTokens+d.OutputTokens)
		}
	}
}

// runServerMode starts the gRPC server in standalone mode (for IDE clients).
func runServerMode(cfg config.Config) {
	fmt.Printf("Starting Cercano gRPC server (v%s)...\n", version)
	fmt.Printf("Local model: %s\n", cfg.LocalModel)
	fmt.Printf("Ollama URL: %s\n", cfg.OllamaURL)

	addr, _, err := startGRPCServer(cfg, ":"+cfg.Port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n[ERROR] %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Server listening at %s\n", addr)

	// Block forever (gRPC server runs in its own goroutine via Serve)
	select {}
}

// runMCPMode starts the MCP server. If no external gRPC address is provided,
// it starts an embedded gRPC server on a random port.
func runMCPMode(cfg config.Config, externalGRPC string) {
	var grpcTarget string

	if externalGRPC != "" {
		// Connect to an external gRPC server
		grpcTarget = externalGRPC
		fmt.Fprintf(os.Stderr, "Cercano MCP server (v%s) connecting to external gRPC at %s...\n", version, grpcTarget)
	} else {
		// Embedded mode: start gRPC server in-process on a random port
		fmt.Fprintf(os.Stderr, "Cercano MCP server (v%s) starting with embedded gRPC server...\n", version)
		fmt.Fprintf(os.Stderr, "Local model: %s | Ollama: %s\n", cfg.LocalModel, cfg.OllamaURL)

		addr, _, err := startGRPCServer(cfg, "localhost:0")
		if err != nil {
			// Start in degraded mode so the MCP pipe stays alive and
			// the client gets a clear error instead of "Failed to reconnect".
			fmt.Fprintf(os.Stderr, "[ERROR] %v — starting in degraded mode\n", err)
			s := mcpserver.NewDegradedServer(err)
			if runErr := s.MCPServer().Run(context.Background(), &gomcp.StdioTransport{}); runErr != nil {
				fmt.Fprintf(os.Stderr, "MCP server error: %v\n", runErr)
				os.Exit(1)
			}
			return
		}
		grpcTarget = addr
		fmt.Fprintf(os.Stderr, "Embedded gRPC server listening at %s\n", grpcTarget)
	}

	// Connect MCP to gRPC
	conn, err := grpc.NewClient(grpcTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to gRPC server at %s: %v\n", grpcTarget, err)
		os.Exit(1)
	}
	defer conn.Close()

	grpcClient := proto.NewAgentClient(conn)
	s := mcpserver.NewServer(grpcClient)

	// Initialize telemetry
	telemetryPath := filepath.Join(filepath.Dir(config.DefaultPath()), "telemetry.db")
	telemetryStore, err := telemetry.NewSQLiteStore(telemetryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to initialize telemetry: %v\n", err)
	} else {
		collector := telemetry.NewCollector(telemetryStore, 256)
		s.SetCollector(collector)
		defer collector.Close()
		defer telemetryStore.Close()

		// Log cumulative stats on startup
		if stats, err := telemetryStore.GetStats(context.Background()); err == nil && stats.TotalRequests > 0 {
			totalLocal := stats.TotalInputTokens + stats.TotalOutputTokens
			fmt.Fprintf(os.Stderr, "Telemetry: %d requests, %d local tokens processed\n", stats.TotalRequests, totalLocal)
		}
	}

	if err := s.MCPServer().Run(context.Background(), &gomcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
