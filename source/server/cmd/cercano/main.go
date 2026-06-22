package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
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
	"strconv"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/cli/agentclient"
	cliui "cercano/source/server/internal/cli/ui"
	"cercano/source/server/internal/config"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/dispatch/builtin"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/engine/ollama"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
	mcpserver "cercano/source/server/internal/mcp"
	"cercano/source/server/internal/recap"
	"cercano/source/server/internal/server"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/tools"
	"cercano/source/server/internal/update"
	"cercano/source/server/pkg/proto"

	tea "github.com/charmbracelet/bubbletea"

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

// setupDispatchLogFile configures the standard library logger to tee output
// to both stderr and ~/.cercano-dispatch.log with microsecond timestamps,
// so dispatch diagnostics survive Claude Code's startup-only stderr capture.
func setupDispatchLogFile() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[diag] could not resolve home dir for log: %v\n", err)
		return
	}
	path := home + "/.cercano-dispatch.log"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[diag] could not open dispatch log %s: %v\n", path, err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("diag: dispatch log opened, version=%s pid=%d", version, os.Getpid())
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

	// Cloud provider: only construct a real one when there's enough config to
	// actually reach a cloud. Otherwise return a sentinel that auto-degrades
	// to local at turn time with a visible scrollback notice. No silent mock.
	var cloudProvider agent.ModelProvider
	canConfigureCloud := cfg.CloudProvider != "" && (cfg.CloudAPIKey != "" || cfg.CloudBaseURL != "")
	if canConfigureCloud {
		fmt.Fprintf(os.Stderr, "Initializing Cloud Provider (%s)...\n", cfg.CloudProvider)
		if cfg.CloudBaseURL != "" {
			fmt.Fprintf(os.Stderr, "  baseURL: %s\n", cfg.CloudBaseURL)
		}
		cp, err := llm.NewCloudModelProvider(context.Background(), cfg.CloudProvider, cfg.CloudModel, cfg.CloudAPIKey, cfg.CloudBaseURL)
		if err == nil {
			cloudProvider = cp
		} else {
			fmt.Fprintf(os.Stderr, "Failed to init Cloud Provider: %v — cloud routing will degrade to local.\n", err)
			cloudProvider = llm.NewAbsentCloudProvider("provider init failed: " + err.Error())
		}
	}
	if cloudProvider == nil {
		reason := "no API key or base URL configured"
		if cfg.CloudProvider == "" {
			reason = "no provider selected"
		}
		cloudProvider = llm.NewAbsentCloudProvider(reason)
		fmt.Fprintf(os.Stderr, "Cloud provider absent (%s); routing will degrade to local with notice.\n", reason)
	}

	validator := tools.NewAutoValidator(tools.DefaultLoader(), tools.DefaultKindToValidator())
	sessionSvc := session.InMemoryService()
	coordinator := loop.NewADKCoordinator(localProvider, cloudProvider, validator, sessionSvc)

	cloudFactory := func(ctx context.Context, provider, model, apiKey, baseURL string) (agent.ModelProvider, error) {
		return llm.NewCloudModelProvider(ctx, provider, model, apiKey, baseURL)
	}

	// SmartRouter is built lazily on first use. This keeps MCP-only deployments
	// working even when the embedding model (nomic-embed-text) is not installed,
	// since MCP tools never classify intent. Prototypes are embedded in the
	// binary (see //go:embed in internal/agent/router.go). See GitHub issue #5.
	routerFactory := func() (*agent.SmartRouter, error) {
		return agent.NewSmartRouterFromBytes(localProvider, cloudProvider, cfg.EmbeddingModel, ollamaEng, agent.DefaultPrototypes(), cloudFactory)
	}
	lazyRouter := agent.NewLazyRouter(routerFactory, localProvider, cloudProvider)

	convStore := agent.NewConversationStore(sessionSvc, 3)

	// Persistent SQLite-backed conversation store for /history and /resume.
	// If the open fails (disk full, perms), log and continue without
	// persistence — the agent still works for transient turns.
	var persistentStore conversation.Store
	if path, err := conversation.DefaultPath(); err == nil {
		if ps, err := conversation.Open(path); err == nil {
			persistentStore = ps
			fmt.Fprintf(os.Stderr, "Conversation store: %s\n", path)
		} else {
			fmt.Fprintf(os.Stderr, "[WARN] Failed to open conversation store at %s: %v — /history & /resume disabled.\n", path, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[WARN] Could not resolve conversation store path: %v — /history & /resume disabled.\n", err)
	}

	// Context-window meter: per-conversation running token counters keyed
	// by conversation id. The CLI polls GetContextUsage after each turn.
	meterRegistry := contextmeter.NewRegistry()

	// Project context loader: reads .cercano/context.md from the request's
	// WorkDir and prepends it to the prompt so the model has project
	// awareness on every turn.
	ctxLoader := projectctx.NewLoader()

	agentOpts := []agent.AgentOption{
		agent.WithConversationStore(convStore),
		agent.WithPersistentStore(persistentStore),
		agent.WithContextMeter(meterRegistry, cfg.LocalModel),
		agent.WithContextLoader(ctxLoader),
	}
	// Living recap: after each turn, a debounced local-model pass updates a
	// one-line conversation summary. Only when a persistent store exists.
	if persistentStore != nil {
		recapComplete := func(ctx context.Context, prompt string) (string, error) {
			resp, err := localProvider.Process(ctx, &agent.Request{Input: prompt})
			if err != nil {
				return "", err
			}
			return resp.Output, nil
		}
		recapGen := recap.New(persistentStore, recapComplete, 8*time.Second, 12)
		agentOpts = append(agentOpts, agent.WithRecapScheduler(recapGen))
	}
	orchestrator := agent.NewAgent(lazyRouter, coordinator, agentOpts...)

	lis, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return "", nil, fmt.Errorf("failed to listen on %s: %v", bindAddr, err)
	}

	s := grpc.NewServer()
	srv := server.NewServer(orchestrator, localProvider, lazyRouter, coordinator, cloudFactory, registry)
	srv.SetConfigPersistence(config.DefaultPath(), cfg)
	srv.SetToolRegistry(agenttools.DefaultRegistry())
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
	// Handle subcommands before flag parsing.
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
		case "agent":
			// Explicit server mode: starts the gRPC agent in the foreground.
			// Used by `cercano-cli` auto-launch and by IDE extensions that want
			// to manage the server lifecycle themselves.
			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "[WARN] Failed to load config: %v (using defaults)\n", err)
				cfg = config.Defaults()
			}
			runServerMode(cfg)
			return
		}
	}

	mcpMode := flag.Bool("mcp", false, "Run in MCP mode (embedded gRPC server + MCP on stdio)")
	grpcAddr := flag.String("grpc-addr", "", "Address of an external gRPC server (MCP-only, no embedded server)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	showStats := flag.Bool("stats", false, "Print usage statistics and exit")
	forceServer := flag.Bool("server", false, "Force server mode (equivalent to `cercano agent`)")
	forceCLI := flag.Bool("cli", false, "Force CLI mode even when stdin is not a terminal")
	resumeOnStart := flag.Bool("r", false, "Open the conversation history picker on launch (alias for --resume)")
	resumeLong := flag.Bool("resume", false, "Open the conversation history picker on launch")
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

	// Load config (server + CLI both need it).
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to load config: %v (using defaults)\n", err)
		cfg = config.Defaults()
	}

	openHistory := *resumeOnStart || *resumeLong
	switch {
	case *mcpMode:
		runMCPMode(cfg, *grpcAddr)
	case *forceServer:
		runServerMode(cfg)
	case *forceCLI:
		runCLI(cfg, openHistory)
	case isInteractiveStdin():
		// Real terminal invocation → the user wants the CLI.
		runCLI(cfg, openHistory)
	default:
		// Spawned by another process (VS Code extension, MCP host wrapper,
		// systemd, etc.) → run the gRPC server in the foreground so the
		// parent process gets what it expects.
		runServerMode(cfg)
	}
}

// isInteractiveStdin reports whether stdin is connected to a character device,
// i.e. a real terminal. Returns false for pipes, files, sockets, and the
// no-TTY contexts that VS Code's extension host, MCP stdio hosts, and CI
// runners use to spawn child processes. Forces CLI mode → server mode when
// nothing's there to drive a TUI.
func isInteractiveStdin() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// runCLI launches the cercano TUI. Connects to a running agent on
// localhost:<cfg.Port>, or auto-launches one (as `cercano agent`) on miss.
// openHistoryOnStart opens the /history picker immediately after first paint
// (used by the -r / --resume flag).
func runCLI(cfg config.Config, openHistoryOnStart bool) {
	addr := "localhost:" + cfg.Port
	fmt.Fprintln(os.Stderr, "cercano: connecting to", addr+"…")
	ag, err := agentclient.Dial(context.Background(), addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cercano:", err)
		os.Exit(1)
	}
	defer ag.Close()
	if ag.AutoLaunched {
		fmt.Fprintln(os.Stderr, "cercano: auto-launched agent server (log:", ag.ServerLog+")")
	}

	m := cliui.New(ag, openHistoryOnStart)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "cercano:", err)
		os.Exit(1)
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
	fmt.Printf("\n[1/7] Checking for AI engine backends...\n")
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

	// Step 2: Check/choose a chat model.
	//
	// Cercano does not prescribe a particular chat model — the user picks
	// whatever fits their hardware and workload. Setup only asks the user
	// to choose when Ollama has no installed chat models at all.
	if engineAvailable {
		fmt.Println("\n[2/7] Checking chat models...")

		installed, err := listInstalledModels(cfg.OllamaURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  FAIL: Could not list models: %v\n", err)
			os.Exit(1)
		}

		// The embedding model (nomic-embed-text) is not a chat model — it's
		// only needed for gRPC agent-mode routing (VS Code/Zed) and is
		// offered as a separate opt-in step below.
		chatModels := filterChatModels(installed)

		if len(chatModels) == 0 {
			// No chat models available — show curated picker.
			picked, err := pickCuratedModel(os.Stdin, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  FAIL: %v\n", err)
				os.Exit(1)
			}
			if picked != "" {
				fmt.Printf("  Pulling %s (this can take several minutes)...\n", picked)
				if err := pullModel(cfg.OllamaURL, picked); err != nil {
					fmt.Fprintf(os.Stderr, "  FAIL: Could not pull %s: %v\n", picked, err)
					os.Exit(1)
				}
				fmt.Printf("  OK: %s pulled.\n", picked)
				cfg.LocalModel = picked
			} else {
				fmt.Fprintln(os.Stderr, "  Skipping model pull. Pull a chat model with `ollama pull <model>` and re-run `cercano setup`.")
			}
		} else {
			// Use the configured model if it's installed; otherwise fall
			// back to the first installed chat model and update the config.
			configured := strings.TrimSuffix(cfg.LocalModel, ":latest")
			configuredPresent := false
			for _, m := range chatModels {
				if strings.TrimSuffix(m, ":latest") == configured {
					configuredPresent = true
					break
				}
			}
			if configuredPresent {
				fmt.Printf("  OK: Using %s (from config).\n", cfg.LocalModel)
			} else {
				chosen := chatModels[0]
				fmt.Printf("  Configured model %q not installed. Using %s instead.\n", cfg.LocalModel, chosen)
				cfg.LocalModel = chosen
			}

			if len(chatModels) > 1 {
				fmt.Printf("  Other installed chat models: %s\n", strings.Join(chatModels[1:], ", "))
				fmt.Println("  (Change the active model anytime with `cercano_config set local_model <name>`.)")
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "\n[2/7] Skipping model check (no engine available).")
	}

	// Check/create config file
	fmt.Println("\n[3/7] Checking config file...")
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
	fmt.Println("\n[4/7] Checking Claude Code telemetry hook...")
	if err := ensureClaudeHook(); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not configure hook: %v\n", err)
	}

	// Set up Python venv for web research (DuckDuckGo search)
	fmt.Println("\n[5/7] Setting up Python venv for web research...")
	if err := ensureVenv(); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not set up Python venv: %v\n", err)
		fmt.Fprintf(os.Stderr, "  (Web research features will not be available. You can re-run 'cercano setup' to retry.)\n")
	}

	// Optional: agent-mode routing requires an embedding model. Most users
	// (MCP plugins for Claude Code, Codex, Cursor, Windsurf, Gemini CLI) do
	// not need this — only the VS Code and Zed IDE extensions that talk to
	// the gRPC agent API use classification.
	fmt.Println("\n[6/7] Agent-mode routing (optional)...")
	if engineAvailable {
		installed, err := listInstalledModels(cfg.OllamaURL)
		if err == nil {
			embedModel := strings.TrimSuffix(cfg.EmbeddingModel, ":latest")
			hasEmbedding := false
			for _, m := range installed {
				if strings.TrimSuffix(m, ":latest") == embedModel {
					hasEmbedding = true
					break
				}
			}
			if hasEmbedding {
				fmt.Printf("  OK: Embedding model %s is installed — agent-mode routing available.\n", cfg.EmbeddingModel)
			} else {
				fmt.Println("  MCP plugins (Claude Code, Codex, Cursor, Windsurf, Gemini CLI) do not need this.")
				fmt.Println("  Only the VS Code and Zed IDE extensions that use gRPC agent-mode need an embedding model.")
				if promptYesNo(os.Stderr, os.Stdin, fmt.Sprintf("  Pull %s now? [y/N]: ", cfg.EmbeddingModel), false) {
					fmt.Printf("  Pulling %s...\n", cfg.EmbeddingModel)
					if err := pullModel(cfg.OllamaURL, cfg.EmbeddingModel); err != nil {
						fmt.Fprintf(os.Stderr, "  WARN: Could not pull %s: %v\n", cfg.EmbeddingModel, err)
					} else {
						fmt.Printf("  OK: %s pulled.\n", cfg.EmbeddingModel)
					}
				} else {
					fmt.Println("  Skipping. Run `ollama pull " + cfg.EmbeddingModel + "` later to enable agent mode.")
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "  WARN: Could not check for embedding model: %v\n", err)
		}
	} else {
		fmt.Println("  Skipped (no engine available).")
	}

	// Persist any chat-model choice made in step 2.
	if err := config.Save(cfg, configPath); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not save config: %v\n", err)
	}

	// Summary
	fmt.Println("\n[7/7] Setup complete!")
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

// listInstalledModels queries the Ollama /api/tags endpoint and returns
// the list of installed model names (with any :tag suffix preserved).
func listInstalledModels(ollamaURL string) ([]string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(ollamaURL + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := decodeJSON(resp.Body, &body); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// embeddingModelNames are recognized embedding models that should NOT be
// treated as chat models. Cercano only ships with nomic-embed-text support
// today, but excluding other common embedders future-proofs the check.
var embeddingModelNames = map[string]bool{
	"nomic-embed-text":       true,
	"mxbai-embed-large":      true,
	"all-minilm":             true,
	"snowflake-arctic-embed": true,
}

// filterChatModels returns only models that can serve chat/completion
// requests — i.e., anything that isn't an embedding model.
func filterChatModels(installed []string) []string {
	var chat []string
	for _, m := range installed {
		base := strings.TrimSuffix(m, ":latest")
		// Strip any :tag to get the family name for embedding check.
		family := base
		if idx := strings.Index(base, ":"); idx > 0 {
			family = base[:idx]
		}
		if embeddingModelNames[family] {
			continue
		}
		chat = append(chat, m)
	}
	return chat
}

// curatedModel is a Cercano-recommended chat model surfaced when a fresh
// Ollama install has nothing else to suggest.
type curatedModel struct {
	Name        string
	Size        string
	Description string
}

// curatedChatModels are Cercano-recommended chat models shown to users with
// empty Ollama installs. Keep the list short and explicit — users with
// specific preferences can always pick their own with `ollama pull`.
var curatedChatModels = []curatedModel{
	{
		Name:        "qwen3-coder-next:latest",
		Size:        "~18GB",
		Description: "Best for code-heavy Cercano workflows — code explanation, extraction from source trees, structured pulls from technical docs.",
	},
	{
		Name:        "qwen3.6:27b",
		Size:        "~17GB",
		Description: "Best general-purpose — strong reasoning and writing. Great for research, synthesis, and summarization.",
	},
	{
		Name:        "gemma4:26b",
		Size:        "~16GB",
		Description: "Deep research and long-context analysis. Follows structured output formats reliably.",
	},
	{
		Name:        "gemma4:e4b",
		Size:        "~3GB",
		Description: "Tiny efficient variant — runs on older hardware or low-memory machines. Good quality for its size.",
	},
	{
		Name:        "phi4:14b",
		Size:        "~9GB",
		Description: "Mid-size sweet spot — decent reasoning with a smaller footprint than qwen3.6.",
	},
}

// pickCuratedModel shows the curated list and returns the user's selection.
// Returns an empty string if the user declines to pick anything.
func pickCuratedModel(in io.Reader, out io.Writer) (string, error) {
	fmt.Fprintln(out, "  No chat models installed. Choose one (or skip and install your own later):")
	fmt.Fprintln(out)
	for i, m := range curatedChatModels {
		fmt.Fprintf(out, "    [%d] %s (%s)\n", i+1, m.Name, m.Size)
		fmt.Fprintf(out, "        %s\n\n", m.Description)
	}
	fmt.Fprintf(out, "    [0] Skip — I'll install my own with `ollama pull <model>`\n\n")
	fmt.Fprintf(out, "  Choice [0-%d]: ", len(curatedChatModels))

	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("failed to read choice: %w", err)
	}
	choice, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || choice < 0 || choice > len(curatedChatModels) {
		return "", fmt.Errorf("invalid choice %q", strings.TrimSpace(line))
	}
	if choice == 0 {
		return "", nil
	}
	return curatedChatModels[choice-1].Name, nil
}

// promptYesNo reads a y/n response from stdin. Returns defaultYes on empty input.
func promptYesNo(out io.Writer, in io.Reader, prompt string, defaultYes bool) bool {
	fmt.Fprint(out, prompt)
	reader := bufio.NewReader(in)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
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
	// Tee diagnostic logging to a file so it survives Claude Code's stdio
	// stderr capture window (which closes after the initial connect handshake).
	setupDispatchLogFile()

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

	// Wire cercano_dispatch: agentic local-LLM tool-use loop. Uses its own
	// Ollama engine + in-memory session store (separate from the gRPC server's).
	dispatchEng := ollama.NewOllamaEngine(cfg.OllamaURL)
	dispatchReg := dispatch.NewRegistry()
	_ = dispatchReg.Register(builtin.NewReadFile())
	_ = dispatchReg.Register(builtin.NewWriteFile())
	_ = dispatchReg.Register(builtin.NewShellExec())
	_ = dispatchReg.Register(builtin.NewWebFetch())
	dispatchLoop := dispatch.NewLoop(dispatchEng, dispatchReg, cfg.LocalModel, 50)
	dispatchStore := dispatch.NewStore(session.InMemoryService(), 200)
	s.SetDispatch(dispatchLoop, dispatchStore)

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
