package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/config"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/engine/ollama"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
	mcpserver "cercano/source/server/internal/mcp"
	"cercano/source/server/internal/server"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/tools"
	"cercano/source/server/internal/update"
	"cercano/source/server/pkg/proto"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// version is set at build time via -ldflags "-X main.version=...".
// Falls back to "dev" for local builds.
var version = "dev"

func init() {
	// Normalize: strip leading "v" so the print format "v%s" doesn't double up.
	version = strings.TrimPrefix(version, "v")
}

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

	registry := engine.NewEngineRegistry()
	ollamaEng := ollama.NewOllamaEngine(cfg.OllamaURL)
	registry.RegisterEngine(ollamaEng)
	registry.RegisterEmbedder(ollamaEng)

	localProvider := llm.NewLocalModelProvider(ollamaEng, cfg.LocalModel)

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

	// Prototypes are embedded in the binary (see //go:embed in internal/agent/router.go)
	// so the server works from any install location without a sibling data file.
	smartRouter, err := agent.NewSmartRouterFromBytes(localProvider, cloudProvider, cfg.EmbeddingModel, ollamaEng, agent.DefaultPrototypes(), cloudFactory)
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
	srv := server.NewServer(orchestrator, localProvider, smartRouter, coordinator, cloudFactory, registry)
	srv.SetConfigPersistence(config.DefaultPath(), cfg)
	proto.RegisterAgentServer(s, srv)

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
			installEngine := false
			for _, arg := range os.Args[2:] {
				if arg == "--install-engine" {
					installEngine = true
				}
			}
			runSetup(installEngine)
			return
		case "version":
			fmt.Printf("cercano v%s\n", version)
			if info := update.CheckForUpdate(version); info != nil {
				if info.UpdateAvailable {
					fmt.Printf("\nA newer version is available: v%s\n", info.LatestVersion)
					fmt.Printf("  Upgrade: %s\n", info.UpgradeCommand())
					fmt.Printf("  Release: %s\n", info.ReleaseURL)
				} else {
					fmt.Println("(up to date)")
				}
			}
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
		if info := update.CheckForUpdate(version); info != nil {
			if info.UpdateAvailable {
				fmt.Printf("\nA newer version is available: v%s\n", info.LatestVersion)
				fmt.Printf("  Upgrade: %s\n", info.UpgradeCommand())
				fmt.Printf("  Release: %s\n", info.ReleaseURL)
			} else {
				fmt.Println("(up to date)")
			}
		}
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
func runSetup(installEngine bool) {
	fmt.Printf("Cercano Setup (v%s)\n", version)

	// Check for updates (cached, non-blocking)
	configDir := filepath.Dir(config.DefaultPath())
	if info := update.CheckCached(version, configDir); info != nil && info.UpdateAvailable {
		fmt.Printf("\n  Note: A newer version is available (v%s).\n", info.LatestVersion)
		fmt.Printf("  Run `%s` after setup to get the latest features.\n", info.UpgradeCommand())
	}

	fmt.Println("\nChecking prerequisites...")

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		cfg = config.Defaults()
	}

	// Step 1: Detect AI engine backend
	fmt.Printf("\n[1/6] Checking for AI engine backends...\n")
	detection := detectEngineWith(checkOllama, cfg.OllamaURL)

	engineAvailable := detection.Available
	if detection.Available {
		fmt.Printf("  OK: %s is running at %s\n", detection.Name, detection.URL)
	} else {
		// Prompt for installation
		shouldInstall := promptInstallEngine(os.Stderr, os.Stdin, installEngine)
		if shouldInstall {
			goos := runtime.GOOS
			hasBrew := hasBrewInstalled()
			if err := installOllama(goos, hasBrew); err != nil {
				fmt.Fprintf(os.Stderr, "  FAIL: %v\n", err)
				fmt.Fprintf(os.Stderr, "  Install Ollama manually from https://ollama.com/download and re-run 'cercano setup'.\n")
				os.Exit(1)
			}
			// Start Ollama after install
			if err := startOllama(goos, hasBrew); err != nil {
				fmt.Fprintf(os.Stderr, "  WARN: %v\n", err)
				fmt.Fprintf(os.Stderr, "  Please start Ollama manually and re-run 'cercano setup'.\n")
				os.Exit(1)
			}
			// Wait for engine to become responsive
			if err := waitForEngine(checkOllama, cfg.OllamaURL, 10); err != nil {
				fmt.Fprintf(os.Stderr, "  FAIL: %v\n", err)
				fmt.Fprintf(os.Stderr, "  Ollama was installed but is not responding. Please start it manually and re-run 'cercano setup'.\n")
				os.Exit(1)
			}
			fmt.Fprintln(os.Stderr, "  OK: Ollama is running.")
			engineAvailable = true
		} else {
			fmt.Fprintln(os.Stderr, "  Skipping engine installation.")
			fmt.Fprintln(os.Stderr, "  Install Ollama from https://ollama.com/download when ready, then re-run 'cercano setup'.")
		}
	}

	// Step 2: Check required models (only if engine is available)
	if engineAvailable {
		fmt.Println("\n[2/6] Checking required models...")
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
	} else {
		fmt.Fprintln(os.Stderr, "\n[2/6] Skipping model check (no engine available).")
	}

	// Check/create config file
	fmt.Println("\n[3/6] Checking config file...")
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

	// Configure Claude Code hook for cloud token telemetry
	fmt.Println("\n[4/6] Checking Claude Code telemetry hook...")
	if err := ensureClaudeHook(); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not configure hook: %v\n", err)
	}

	// Set up Python venv for web research (DuckDuckGo search)
	fmt.Println("\n[5/6] Setting up Python venv for web research...")
	if err := ensureVenv(); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not set up Python venv: %v\n", err)
		fmt.Fprintf(os.Stderr, "  (Web research features will not be available. You can re-run 'cercano setup' to retry.)\n")
	}

	// Summary
	fmt.Println("\n[6/6] Setup complete!")
	if engineAvailable {
		fmt.Println("  Run 'cercano' to start the server.")
	} else {
		fmt.Println("  Note: No AI engine is installed. Install Ollama from https://ollama.com/download")
		fmt.Println("  then re-run 'cercano setup' to pull models.")
	}
}

// ensureVenv creates the Python venv at ~/.config/cercano/venv/ and installs
// ddgs if not already set up. Validates the install with a test import.
func ensureVenv() error {
	venvDir := config.VenvDir()
	pythonPath := config.VenvPython()

	// Check if venv already exists and is working
	if _, err := os.Stat(pythonPath); err == nil {
		// Validate the existing venv has ddgs
		cmd := exec.Command(pythonPath, "-c", "import ddgs")
		if cmd.Run() == nil {
			fmt.Println("  OK: Python venv exists and ddgs is installed.")
			return nil
		}
		fmt.Println("  Venv exists but ddgs is missing — reinstalling...")
	}

	// Find system python3
	systemPython, err := exec.LookPath("python3")
	if err != nil {
		return fmt.Errorf("python3 not found in PATH. Install Python 3 to enable web research features")
	}

	// Create venv
	fmt.Printf("  Creating venv at %s...\n", venvDir)
	cmd := exec.Command(systemPython, "-m", "venv", venvDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create venv: %w\n%s", err, string(out))
	}

	// Install ddgs
	pipPath := filepath.Join(venvDir, "bin", "pip")
	fmt.Println("  Installing ddgs...")
	cmd = exec.Command(pipPath, "install", "--quiet", "ddgs")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install ddgs: %w\n%s", err, string(out))
	}

	// Validate
	cmd = exec.Command(pythonPath, "-c", "import ddgs; print('ok')")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("validation failed: %w\n%s", err, string(out))
	}

	fmt.Println("  OK: Python venv created and ddgs installed.")
	return nil
}

// ensureClaudeHook adds the PostToolUse telemetry hook to Claude Code's
// user-level settings.json if it's not already present.
func ensureClaudeHook() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	// Find the hook script
	exePath, _ := os.Executable()
	hookScript := filepath.Join(filepath.Dir(exePath), "..", "hooks", "report_cloud_tokens.py")
	// Resolve to absolute path
	hookScript, _ = filepath.Abs(hookScript)
	if _, err := os.Stat(hookScript); os.IsNotExist(err) {
		// Try relative to server root
		serverRoot := filepath.Dir(filepath.Dir(exePath))
		hookScript = filepath.Join(serverRoot, "hooks", "report_cloud_tokens.py")
		if _, err := os.Stat(hookScript); os.IsNotExist(err) {
			return fmt.Errorf("hook script not found")
		}
	}

	// Read existing settings
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			settings = make(map[string]interface{})
		} else {
			return err
		}
	} else {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("failed to parse settings.json: %w", err)
		}
	}

	// Check if hook already exists
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}

	postToolUse, _ := hooks["PostToolUse"].([]interface{})
	for _, h := range postToolUse {
		if hm, ok := h.(map[string]interface{}); ok {
			if m, ok := hm["matcher"].(string); ok && m == "mcp__cercano__.*" {
				fmt.Println("  OK: Telemetry hook already configured.")
				return nil
			}
		}
	}

	// Add the hook
	hookEntry := map[string]interface{}{
		"matcher": "mcp__cercano__.*",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": fmt.Sprintf("python3 %s", hookScript),
			},
		},
	}
	postToolUse = append(postToolUse, hookEntry)
	hooks["PostToolUse"] = postToolUse
	settings["hooks"] = hooks

	// Write back
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		return err
	}
	fmt.Printf("  OK: Telemetry hook added (script: %s)\n", hookScript)
	return nil
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

	fmt.Print(telemetry.FormatStatsASCII(stats))
}

// generateSessionID returns a UUID v4 string for identifying an MCP session.
func generateSessionID() string {
	var uuid [16]byte
	rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
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
		collector.SetSessionID(generateSessionID())
		s.SetCollector(collector)
		defer collector.Close()
		defer telemetryStore.Close()

		// Log cumulative stats on startup
		if stats, err := telemetryStore.GetStats(context.Background()); err == nil && stats.TotalRequests > 0 {
			totalLocal := stats.TotalInputTokens + stats.TotalOutputTokens
			fmt.Fprintf(os.Stderr, "Telemetry: %d requests, %d local tokens processed\n", stats.TotalRequests, totalLocal)
		}
	}

	// Check for updates (cached, non-blocking)
	configDir := filepath.Dir(config.DefaultPath())
	if info := update.CheckCached(version, configDir); info != nil && info.UpdateAvailable {
		fmt.Fprintf(os.Stderr, "[UPDATE] A newer version is available: v%s. Run: %s\n", info.LatestVersion, info.UpgradeCommand())
		s.SetUpdateInfo(info.LatestVersion, info.UpgradeCommand())
	}

	if err := s.MCPServer().Run(context.Background(), &gomcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
