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
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactiongen"
	"cercano/source/server/internal/compactor"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/crashlog"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	llamaengine "cercano/source/server/internal/engine/llamaserver"
	"cercano/source/server/internal/engine/ollama"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	ollamallm "cercano/source/server/internal/llm/ollama"
	"cercano/source/server/internal/localruntime"
	runtimellama "cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/loop"
	mcpserver "cercano/source/server/internal/mcp"
	mcphost "cercano/source/server/internal/mcp_host"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/protocols"
	"cercano/source/server/internal/recap"
	"cercano/source/server/internal/retention"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/server"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/tools"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"cercano/source/server/pkg/update"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"cercano/source/server/internal/worker"
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

// ollamaStartupWarning probes Ollama and, if unreachable, returns a warning
// for the startup log. Ollama down is never fatal: the configured runtime may
// be llama_server and the primary route may be cloud, neither of which needs
// it. Ollama-backed features (Ollama chat models, embeddings, the model
// catalog) fail at point-of-use instead, matching the lazy SmartRouter and
// the embedded --mcp degraded-mode paths.
func ollamaStartupWarning(check func(string) error, baseURL string) string {
	if err := check(baseURL); err != nil {
		return fmt.Sprintf("[WARN] Ollama unreachable at %s (%v) — continuing; Ollama-backed models and embeddings unavailable until it comes up.", baseURL, err)
	}
	return ""
}

// drainGrace bounds how long a shutting-down agent waits for in-flight turns
// to finish before hard-stopping. It is a zombie-guard, not a tuning knob: an
// agentic turn legitimately runs for minutes (model calls + tool executions),
// and the drained process holds no resources a replacement needs — the
// listener port frees the moment the drain starts. Only a truly wedged
// handler should ever hit this. A second signal forces immediate exit.
const drainGrace = 10 * time.Minute

// startGRPCServer initializes all providers and starts the gRPC server.
// Returns the listener address and a cleanup function.
func startGRPCServer(cfg config.Config, bindAddr string) (string, func(), error) {
	if warn := ollamaStartupWarning(checkOllama, cfg.OllamaURL); warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}

	registry := engine.NewEngineRegistry()
	ollamaEng := ollama.NewOllamaEngine(cfg.OllamaURL)
	registry.RegisterEngine(ollamaEng)
	registry.RegisterEmbedder(ollamaEng)
	// llamaEng registers below once constructed — see RegisterEmbedder there.

	runtimeManager := buildRuntimeManager(cfg)
	llamaEng := llamaengine.NewEngine(runtimeManager)
	registry.RegisterEngine(llamaEng)
	registry.RegisterEmbedder(llamaEng)

	openEngine, openModel := selectOpenEngine(cfg, ollamaEng, llamaEng)
	openProvider := legacymodels.NewOpenModelProvider(openEngine, openModel)

	// Cloud provider: start with the absent sentinel; RebuildCloud() (called
	// after secrets are wired below) resolves the active profile's key from
	// the OS keychain and installs the real provider.
	cloudProvider := legacymodels.NewAbsentCloudProvider("pending profile resolution")

	validator := tools.NewAutoValidator(tools.DefaultLoader(), tools.DefaultKindToValidator())
	sessionSvc := session.InMemoryService()
	coordinator := loop.NewADKCoordinator(openProvider, cloudProvider, validator, sessionSvc)

	cloudFactory := func(ctx context.Context, provider, model, apiKey, baseURL string) (agent.ModelProvider, error) {
		return legacymodels.NewCloudModelProvider(ctx, provider, model, apiKey, baseURL)
	}

	// SmartRouter is built lazily on first use. This keeps MCP-only deployments
	// working even when the embedding model (nomic-embed-text) is not installed,
	// since MCP tools never classify intent. Prototypes are embedded in the
	// binary (see //go:embed in internal/agent/router.go). See GitHub issue #5.
	routerFactory := func() (*agent.SmartRouter, error) {
		return agent.NewSmartRouterFromBytes(openProvider, cloudProvider, cfg.OpenEmbeddingModel(), selectEmbedEngine(cfg, ollamaEng, llamaEng), agent.DefaultPrototypes(), cloudFactory)
	}
	lazyRouter := agent.NewLazyRouter(routerFactory, openProvider, cloudProvider)

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
		agent.WithContextMeter(meterRegistry, cfg.OpenChatModel()),
		agent.WithContextLoader(ctxLoader),
	}
	// Living recap: after each turn, a debounced local-model pass updates a
	// one-line conversation summary. Only when a persistent store exists.
	if persistentStore != nil {
		// Recap rides the fast_light_text tier's open slot — the taxonomy's
		// prose-judgment lane (watchdog verdicts, summaries, recaps) — so it
		// never depends on the interactive open model, which may be heavy or
		// even unloadable. Unset tier keeps the provider default.
		recapModel := ""
		if id, _, ok := cfg.Models.Resolve(config.TierFastLightText, config.ProviderOpen, true); ok {
			recapModel = id
		}
		recapComplete := func(ctx context.Context, prompt string) (string, error) {
			req := &agent.Request{Input: prompt}
			if recapModel != "" {
				req.ModelOverride = recapModel
			}
			resp, err := openProvider.Process(ctx, req)
			if err != nil {
				return "", err
			}
			return resp.Output, nil
		}
		recapGen := recap.New(persistentStore, recapComplete, 8*time.Second, 12)
		agentOpts = append(agentOpts, agent.WithRecapScheduler(recapGen))
	}
	var compGen *compactiongen.Generator
	if persistentStore != nil {
		// Summarizer model precedence: explicit compaction.summarizer_model →
		// the fast_light_text tier's open side → the interactive open model as
		// the fallback of last resort. Summaries are prose-quality judgment
		// work — the fast_light_text charter — matching recap above (small
		// coder models drop anchors; see capability-tier-audit.md).
		summarizerModel := cfg.Compaction.SummarizerModel
		if summarizerModel == "" {
			if id, _, ok := cfg.Models.Resolve(config.TierFastLightText, config.ProviderOpen, true); ok {
				summarizerModel = id
			}
		}
		compactSummarize := func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
			// Greedy decoding is a correctness requirement here, not a tuning
			// choice: the frames-matrix bakeoff (compaction-bakeoff-findings.md)
			// showed default-temperature summarization is a coin flip — the same
			// window swung 0/7 to 7/7 on anchor retention between samples, while
			// temperature 0 reproduced exactly and kept every proposal anchor.
			greedy := engine.Greedy()
			req := &agent.Request{Input: compaction.BuildSummaryPrompt(msgs), Temperature: greedy.Temperature}
			if summarizerModel != "" {
				req.ModelOverride = summarizerModel
			}
			resp, err := openProvider.Process(ctx, req)
			if err != nil {
				return compaction.StructuredSummary{}, err
			}
			return compaction.ParseSummary(resp.Output), nil
		}
		compCfg := compactor.Config{
			ActivationFloorTokens: cfg.Compaction.ActivationFloorTokens,
			SegmentTokens:         cfg.Compaction.SegmentTokens,
			VerbatimRecent:        cfg.Compaction.VerbatimRecent,
		}
		if summarizerModel == "" {
			fmt.Fprintf(os.Stderr, "[compaction] WARN: no summarizer_model and no models.tiers.fast_light_text.open configured — compaction summarizes with the interactive open model (%s), which can be very slow for large histories\n", cfg.OpenChatModel())
		}
		compGen = compactiongen.New(persistentStore, compactSummarize, compCfg, contextmeter.Default(), 10*time.Second)
		// Runtime kill switch — Schedule noops until enabled. Wiring the
		// scheduler unconditionally lets /config compaction-enabled true flip
		// the switch at runtime; the OLD gate required a restart.
		compGen.SetEnabled(cfg.Compaction.Enabled)
		agentOpts = append(agentOpts, agent.WithCompactionScheduler(compGen))
	}
	var sweeper *retention.Sweeper
	if persistentStore != nil {
		sweeper = retention.New(persistentStore, retention.Config{
			RawRetentionDays:       cfg.Compaction.Retention.RawRetentionDays,
			CompactedRetentionDays: cfg.Compaction.Retention.CompactedRetentionDays,
			KeepForever:            cfg.Compaction.Retention.KeepForever,
		}, 12*time.Hour)
		sweeper.Start(context.Background())
	}
	orchestrator := agent.NewAgent(lazyRouter, coordinator, agentOpts...)

	// Startup orphan-sweep: a hard-killed previous host leaves worker process
	// groups + pidfiles/sockets behind. Reap the ones still alive that identify
	// as cercano workers before we start serving (and before this host spawns
	// any worker of its own). Best-effort — never blocks startup.
	if n := worker.ReapOrphanWorkers(); n > 0 {
		fmt.Fprintf(os.Stderr, "reaped %d orphaned worker(s) from a previous run\n", n)
	}

	lis, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return "", nil, fmt.Errorf("failed to listen on %s: %v", bindAddr, err)
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
	srv := server.NewServer(orchestrator, openProvider, lazyRouter, coordinator, cloudFactory, registry)
	srv.SetRuntimeManager(runtimeManager)

	// Attach the online-catalog manager so the runtime dashboard has
	// Ollama's public library available (in addition to the hardcoded
	// catalog + files on disk). LoadCache is best-effort — a missing
	// cache on first run just means the background refresher will
	// populate it shortly. Start() is what kicks the periodic refresh.
	catalogCachePath := filepath.Join(filepath.Dir(config.DefaultPath()), "catalog-cache.json")
	catalogManager := ollamacatalog.NewManager(&ollamacatalog.Fetcher{}, catalogCachePath)
	if err := catalogManager.LoadCache(); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Could not load online-catalog cache at %s: %v (starting fresh)\n", catalogCachePath, err)
	}
	catalogManager.Start(context.Background())
	srv.SetCatalogManager(catalogManager)
	// Wire the catalog manager into the runtime manager as its OCI
	// resolver — this is what enables DownloadModel to translate an
	// online-catalog ref (e.g. "qwen2.5-coder:7b") into the concrete
	// blob URL via a JIT manifest fetch. Type-asserted because the
	// runtimeManager variable is the interface type; only
	// *InMemoryManager currently implements SetOCIResolver, and if a
	// future alternate implementation lands the assertion just skips
	// the resolver injection (online catalog would list but not
	// download — fail-safe).
	if imm, ok := runtimeManager.(*localruntime.InMemoryManager); ok {
		imm.SetOCIResolver(catalogManager)
	}
	if sweeper != nil {
		srv.SetRetentionSweeper(sweeper)
	}
	if compGen != nil {
		srv.SetCompactionGenerator(compGen)
	}
	srv.SetContextLoader(ctxLoader)
	srv.SetConfigPersistence(config.DefaultPath(), cfg)
	orchestrator.SetLocusModeGetter(srv.LocusMode)

	// Permission store + pending-decisions barrier for the native tool-calling
	// loop. Path mirrors config.yaml: ~/.config/cercano/permissions.yaml.
	permsPath := filepath.Join(filepath.Dir(config.DefaultPath()), "permissions.yaml")
	permStore, err := agent.LoadPermissionStore(permsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to load permission store at %s: %v — defaulting to permissive in-memory store.\n", permsPath, err)
		permStore, _ = agent.LoadPermissionStore(permsPath)
	}
	pending := agent.NewPendingDecisions()
	srv.SetPermissions(permStore, pending)
	// Watch permissions.yaml for out-of-band edits (hand-edits, tools) and push
	// the change to connected clients. Non-fatal: the SetPermissionMode RPC path
	// still broadcasts, and the gate re-reads the file on every decision.
	if err := srv.StartPermissionWatcher(context.Background(), permsPath); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] permission file watcher not started (%v) — /strict etc. still push; hand-edits won't.\n", err)
	}
	// Watch config.yaml for out-of-band edits and replay hot-reloadable fields
	// through UpdateConfig. Non-fatal: the /config RPC path still works.
	if err := srv.StartConfigWatcher(context.Background(), config.DefaultPath()); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] config file watcher not started (%v) — /config still applies live; hand-edits won't.\n", err)
	}

	// Agent-server telemetry: mirrors the MCP-mode collector setup so provider
	// calls in the native tool-calling loop are recorded. The sink is handed to
	// the server, which wraps the selected provider in resolveMainProvider —
	// keeping the server's stored providers raw so the dispatch engine can wrap
	// them per-dispatch without double-counting.
	// agentCollector is hoisted so the coproc engine sink can reference it below.
	var agentCollector *telemetry.Collector
	agentTelemetryPath := filepath.Join(filepath.Dir(config.DefaultPath()), "telemetry.db")
	agentTelemetryStore, err := telemetry.NewSQLiteStore(agentTelemetryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to initialize agent telemetry: %v\n", err)
	} else {
		agentCollector = telemetry.NewCollector(agentTelemetryStore, 256)
		agentCollector.SetSessionID(generateSessionID())
		defer agentCollector.Close()
		defer agentTelemetryStore.Close()
		srv.SetUsageSink(server.UsageEventSink(agentCollector.Emit))
	}

	// Open OS keychain and attach it so profile RPCs and rebuildCloud can
	// read API keys. Failure is non-fatal: cloud stays absent.
	if store, err := secrets.OpenKeychain(); err == nil {
		srv.SetSecrets(store)
	} else {
		fmt.Fprintf(os.Stderr, "[WARN] keychain unavailable (%v) — cloud profiles can't load keys; fallback deferred.\n", err)
	}

	// Wire the Meridian proxy manager BEFORE RebuildCloud so the first
	// resolution of a meridian-routed profile spawns the proxy. Cleanup runs
	// via srv.Shutdown in the cleanup closure below.
	srv.SetupMeridian(filepath.Join(filepath.Dir(config.DefaultPath()), "meridian.log"))

	// One-time relocation: if a legacy inline API key is still in the config,
	// ensure it is safely in the keychain and then blank the YAML field so it
	// never persists in plain text.  Two cases are handled:
	//   1. Key not yet in keychain → Set it, then blank+save.
	//   2. Key already in keychain but yaml still set (previous run saved the
	//      keychain entry but failed to blank the yaml) → blank+save only.
	// We NEVER blank the yaml unless the key is confirmed present in the keychain.
	if cfg.CloudAPIKey != "" && cfg.ActiveCloudProfile != "" {
		if store, err := secrets.OpenKeychain(); err == nil {
			keySafe := false
			if _, gerr := store.Get(cfg.ActiveCloudProfile); gerr == nil {
				// Already in keychain.
				keySafe = true
			} else {
				// Not in keychain yet — store it first.
				if serr := store.Set(cfg.ActiveCloudProfile, cfg.CloudAPIKey); serr == nil {
					keySafe = true
				} else {
					fmt.Fprintf(os.Stderr, "[WARN] keychain Set failed (%v) — plaintext key NOT blanked in config\n", serr)
				}
			}
			if keySafe {
				cfg.CloudAPIKey = ""
				if sverr := config.Save(cfg, config.DefaultPath()); sverr != nil {
					fmt.Fprintf(os.Stderr, "[WARN] config save failed after key relocation (%v) — plaintext key may persist\n", sverr)
				}
			}
		}
	}

	// Resolve the active cloud profile and wire both the legacy and native
	// cloud providers. Errors are logged but not fatal.
	if err := srv.RebuildCloud(); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] cloud profile resolution failed: %v — cloud routing will degrade to local.\n", err)
	}

	// Native tool-loop local provider — follows the configured runtime.
	// Under llama_server the provider resolves/warms instances through the
	// runtime manager per call; otherwise it is the Ollama client. Stored RAW
	// — resolveMainProvider wraps it for usage at hand-off, and the dispatch
	// engine reads it raw and wraps per-dispatch (no double-count). The same
	// factory is installed on the server so an open_runtime change at runtime
	// rebuilds the provider instead of stranding the old lane.
	openProviderFor := func(c config.Config) llm.Provider {
		if strings.EqualFold(c.OpenRuntime, "llama_server") {
			return llamaengine.NewLLMProvider(llamaEng)
		}
		return ollamallm.NewClient(ollamallm.Config{
			BaseURL: c.OllamaURL,
			Model:   c.OpenChatModel(),
		})
	}
	srv.SetOpenLLMProvider(openProviderFor(cfg))
	srv.SetOpenProviderFactory(openProviderFor)

	// Wire the co-processor dispatch engine. Providers are resolved fresh per
	// dispatch from the server's RAW (unwrapped) providers, so a runtime cloud
	// swap (cloud-profile change → RebuildCloud) is honored and the engine's
	// own per-dispatch usage.Wrap doesn't double-count.
	coprocEngine := dispatch.NewEngine(
		func() dispatch.Providers {
			return dispatch.Providers{Cloud: srv.CloudLLMProvider(), Open: srv.OpenLLMProvider()}
		},
		func() locus.Mode { m, _ := locus.ParseMode(srv.LocusMode()); return m },
		ctxLoader,
	)
	// Model resolution is tier-aware and live: DispatchModelFor reads the
	// taxonomy under the config lock per dispatch, so runtime tier/profile
	// changes are honored and no startup-captured cfg value can go stale.
	coprocEngine.SetModelFor(srv.DispatchModelFor)
	// Activate the usage sink so capabilities that set RecordUsage=true (the coproc
	// caps) emit one event per dispatch. processCoproc/research/document leave
	// RecordUsage false, so they stay MCP-side — no double-counting.
	if agentCollector != nil {
		coprocEngine.SetUsageSink(server.UsageEventSink(agentCollector.Emit))
	}
	orchestrator.SetDispatchEngine(coprocEngine)
	srv.SetDispatchEngine(coprocEngine)

	// Build the protocol-supervision watchdog from config (default-OFF). Must run
	// after SetDispatchEngine — the watchdog's fast-model OneShot lane routes
	// through the dispatch engine.
	srv.InitWatchdog()

	// Build the capability registry with live Services (providers, config, and
	// context loader all set above) and wire it as the server's tool registry.
	// Must run after the provider setters so Services carries real values.
	srv.InstallCapabilities()

	// MCP host: load global servers and connect them in the background. Tools
	// register into the same agenttools.Registry as each server lists; a
	// slow/dead server never blocks boot (a call to a not-ready server blocks
	// only that call — see mcphost).
	cfgDir := filepath.Dir(config.DefaultPath())
	mcpMgr := mcphost.New(srv.ToolRegistry(), cfgDir, 10*time.Second)
	srv.SetMcpManager(mcpMgr)
	mcpCtx, mcpCancel := context.WithCancel(context.Background())
	mcpMgr.Start(mcpCtx)

	// Select worker vs in-process turn execution from the loaded config
	// (default: worker → crash isolation). Runs AFTER config, permissions,
	// and secrets are wired so the worker runner has everything it needs to
	// pre-assemble history, build the ConfigSnapshot, and resolve credentials.
	// Tests never reach this path (they build Server directly), so they stay
	// in-process and never spawn worker processes.
	srv.SelectExecutionMode()

	proto.RegisterAgentServer(s, srv)

	go func() {
		if err := s.Serve(lis); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	cleanup := func() {
		// Drain before teardown: in-flight turns still need the MCP manager
		// and providers, so those stop only after the streams finish. The
		// standing SubscribeEvents streams must end first or GracefulStop
		// waits on attached clients forever.
		srv.BeginShutdown()
		server.DrainThenStop(s, drainGrace)
		mcpCancel()
		mcpMgr.Stop()
		srv.Shutdown()
	}

	return lis.Addr().String(), cleanup, nil
}

func buildRuntimeManager(cfg config.Config) localruntime.Manager {
	manager := localruntime.NewManager(localruntime.WithEndpoints(localruntime.EndpointsFromConfig(cfg)))
	sweepStalePartials(cfg, manager)
	provider := runtimellama.NewProvider(cfg.LlamaServer)
	// Reap llama-servers orphaned by cercano processes that died without
	// cleanup — before the enabled check, because orphans from when the
	// runtime WAS enabled hold GPU memory regardless of current config.
	provider.SweepOrphans(manager)
	manager.RegisterProvider(provider)
	if !llamaServerEnabled(cfg) {
		return manager
	}
	if strings.TrimSpace(cfg.LlamaServer.DefaultModel) == "" {
		manager.WriteLog(localruntime.LogEntry{
			Source:  "cercano.runtime.llama_server",
			Level:   "info",
			Message: "llama-server provider registered; no default_model configured",
		})
		return manager
	}
	startDefaultRuntimeAsync(manager, cfg.LlamaServer.DefaultModel)
	return manager
}

// startDefaultRuntimeAsync warms the llama-server default model in the
// background. The warm-up must not run synchronously in startGRPCServer: a
// multi-gigabyte GGUF load takes far longer than the CLI's 8-second
// auto-launch window, and a blocked warm-up holds the gRPC port unbound the
// whole time. Requests that arrive before the model is ready fail with the
// runtime's not-ready error; readiness surfaces through the runtime-status
// event stream like every other runtime state change.
func startDefaultRuntimeAsync(manager localruntime.Manager, modelID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		if _, err := manager.Start(ctx, localruntime.StartRequest{
			Runtime: "llama_server",
			ModelID: modelID,
		}); err != nil {
			manager.WriteLog(localruntime.LogEntry{
				Source:  "cercano.runtime.llama_server",
				Level:   "error",
				Message: "failed to start llama-server: " + err.Error(),
			})
		}
	}()
}

// sweepStalePartials removes .part files older than a week from the
// configured model directories. Recent partials are kept — they feed
// download resume. Orphans happen when the server process dies mid-
// download (no clean failure path runs).
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

// selectEmbedEngine picks the embedder for the configured runtime —
// embeddings run on whatever local runtime is set up, not hardwired to
// Ollama (local-model-taxonomy design).
func selectEmbedEngine(cfg config.Config, ollamaEng engine.EmbeddingService, llamaEng engine.EmbeddingService) engine.EmbeddingService {
	if cfg.OpenRuntime == "llama_server" {
		return llamaEng
	}
	return ollamaEng
}

func selectOpenEngine(cfg config.Config, ollamaEng engine.InferenceEngine, llamaEng engine.InferenceEngine) (engine.InferenceEngine, string) {
	if strings.EqualFold(cfg.OpenRuntime, "llama_server") {
		model := strings.TrimSpace(cfg.LlamaServer.DefaultModel)
		if model == "" {
			model = cfg.OpenChatModel()
		}
		return llamaEng, model
	}
	return ollamaEng, cfg.OpenChatModel()
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
		case "logs":
			// cercano logs --crashes [--tail N] — inspect the server's
			// persistent crash log. See runLogs for the actual flag
			// parsing and output format.
			runLogs(os.Args[2:])
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
		case "worker":
			// Isolated turn-execution worker: serves the Worker gRPC service on a
			// unix socket, runs ONE conversation's turns, proxies state to the
			// host. Spawned by the host's workerRunner — not user-invoked.
			runWorkerMode(os.Args[2:])
			return
		case "run":
			// Headless one-shot: sends a single prompt and streams the response.
			// Intended for scripts and CI — no TTY, no UI.
			runHeadless(os.Args[2:])
			return
		case "protocols":
			// cercano protocols sync [dir] — write protocol SKILL.md files for host discovery.
			if len(os.Args) >= 3 && os.Args[2] == "sync" {
				root := "."
				if len(os.Args) >= 4 {
					root = os.Args[3]
				}
				written, err := protocols.WriteSkillFiles(root)
				if err != nil {
					fmt.Fprintln(os.Stderr, "protocols sync:", err)
					os.Exit(1)
				}
				fmt.Printf("Wrote %d protocol skill files under %s\n", len(written), root)
				return
			}
			fmt.Fprintln(os.Stderr, "usage: cercano protocols sync [dir]")
			os.Exit(2)
		}
	}

	mcpMode := flag.Bool("mcp", false, "Run in MCP mode (embedded gRPC server + MCP on stdio)")
	grpcAddr := flag.String("grpc-addr", "", "Address of an external gRPC server (MCP-only, no embedded server)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	showStats := flag.Bool("stats", false, "Print usage statistics and exit")
	forceServer := flag.Bool("server", false, "Force server mode (equivalent to `cercano agent`)")
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

	// Load config for server/MCP mode.
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to load config: %v (using defaults)\n", err)
		cfg = config.Defaults()
	}

	// This binary IS the cercano agent server. The terminal UI lives in the
	// separate `cercano-cli` binary (source/clients/cli), which dials this
	// server over gRPC and auto-launches `cercano agent` if none is running.
	// The agent server is a singleton; bare `cercano`, `cercano agent`, and
	// `cercano --server` all run it in the foreground.
	switch {
	case *mcpMode:
		runMCPMode(cfg, *grpcAddr)
	case *forceServer:
		runServerMode(cfg)
	default:
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
	fmt.Printf("\n[1/8] Checking for AI engine backends...\n")
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
		fmt.Println("\n[2/8] Checking chat models...")

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
				cfg.Models.Tiers.Everyday.Open = picked
			} else {
				fmt.Fprintln(os.Stderr, "  Skipping model pull. Pull a chat model with `ollama pull <model>` and re-run `cercano setup`.")
			}
		} else {
			// Use the configured model if it's installed; otherwise fall
			// back to the first installed chat model and update the config.
			configured := strings.TrimSuffix(cfg.OpenChatModel(), ":latest")
			configuredPresent := false
			for _, m := range chatModels {
				if strings.TrimSuffix(m, ":latest") == configured {
					configuredPresent = true
					break
				}
			}
			if configuredPresent {
				fmt.Printf("  OK: Using %s (from config).\n", cfg.OpenChatModel())
			} else {
				chosen := chatModels[0]
				fmt.Printf("  Configured model %q not installed. Using %s instead.\n", cfg.OpenChatModel(), chosen)
				cfg.Models.Tiers.Everyday.Open = chosen
			}

			if len(chatModels) > 1 {
				fmt.Printf("  Other installed chat models: %s\n", strings.Join(chatModels[1:], ", "))
				fmt.Println("  (Change the active model anytime with `cercano_config set local_model <name>`.)")
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "\n[2/8] Skipping model check (no engine available).")
	}

	// Step 3: Prepare the optional managed llama-server runtime. This does not
	// change the active inference engine yet; it only makes the supervised
	// sidecar available for dashboard/start-stop flows.
	fmt.Println("\n[3/8] Setting up managed llama-server runtime...")
	ensureLlamaServerSetup(&cfg, installEngine)

	// Check/create config file
	fmt.Println("\n[4/8] Checking config file...")
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
	fmt.Println("\n[5/8] Checking Claude Code telemetry hook...")
	if err := ensureClaudeHook(); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not configure hook: %v\n", err)
	}

	// Set up Python venv for web research (DuckDuckGo search)
	fmt.Println("\n[6/8] Setting up Python venv for web research...")
	if err := ensureVenv(); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not set up Python venv: %v\n", err)
		fmt.Fprintf(os.Stderr, "  (Web research features will not be available. You can re-run 'cercano setup' to retry.)\n")
	}

	// Ensure the embedding model is present. It powers smart routing today and
	// gives semantic features a known local embedder on first run.
	fmt.Println("\n[7/8] Checking embedding model...")
	if cfg.OpenRuntime == "llama_server" {
		// Embeddings ride the llama-server runtime — the encoder GGUF in
		// model_dirs serves them; no Ollama daemon or pull is involved.
		fmt.Printf("  Skipped Ollama pull: embeddings run on llama-server (%s).\n", cfg.OpenEmbeddingModel())
	} else if engineAvailable {
		installed, err := listInstalledModels(cfg.OllamaURL)
		if err == nil {
			if hasInstalledModel(installed, cfg.OpenEmbeddingModel()) {
				fmt.Printf("  OK: Embedding model %s is installed.\n", cfg.OpenEmbeddingModel())
			} else {
				fmt.Printf("  Pulling embedding model %s...\n", cfg.OpenEmbeddingModel())
				if err := pullModel(cfg.OllamaURL, cfg.OpenEmbeddingModel()); err != nil {
					fmt.Fprintf(os.Stderr, "  WARN: Could not pull %s: %v\n", cfg.OpenEmbeddingModel(), err)
				} else {
					fmt.Printf("  OK: %s pulled.\n", cfg.OpenEmbeddingModel())
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
	fmt.Println("\n[8/8] Setup complete!")
	if engineAvailable {
		fmt.Println("  Run 'cercano' to start the server.")
	} else {
		fmt.Println("  Note: No AI engine is installed. Install Ollama from https://ollama.com/download")
		fmt.Println("  then re-run 'cercano setup' to pull models.")
	}
}

func ensureLlamaServerSetup(cfg *config.Config, installEngine bool) {
	applyLlamaServerSetupDefaults(cfg)
	if err := ensureLlamaModelDirs(cfg.LlamaServer.ModelDirs); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not prepare model directory: %v\n", err)
	} else {
		fmt.Printf("  OK: GGUF model directory ready at %s\n", cfg.LlamaServer.ModelDirs[0])
	}

	binary, err := findLlamaServerBinary(cfg.LlamaServer)
	if err != nil {
		shouldInstall := promptInstallLlamaServer(os.Stderr, os.Stdin, installEngine)
		if shouldInstall {
			goos := runtime.GOOS
			hasBrew := hasBrewInstalled()
			if installErr := installLlamaServerRuntime(goos, hasBrew); installErr != nil {
				fmt.Fprintf(os.Stderr, "  WARN: %v\n", installErr)
				fmt.Fprintln(os.Stderr, "  Managed llama-server will stay disabled until `llama-server` is on PATH.")
				cfg.LlamaServer.Enabled = false
				return
			}
			binary, err = findLlamaServerBinary(cfg.LlamaServer)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  WARN: llama-server install completed but binary detection failed: %v\n", err)
				cfg.LlamaServer.Enabled = false
				return
			}
		} else {
			fmt.Fprintln(os.Stderr, "  Skipping managed runtime installation.")
			fmt.Fprintln(os.Stderr, "  Install llama.cpp later and re-run `cercano setup` to enable supervised GGUF models.")
			cfg.LlamaServer.Enabled = false
			return
		}
	}

	cfg.LlamaServer.Enabled = true
	cfg.LlamaServer.Binary = binary
	fmt.Printf("  OK: llama-server runtime available at %s\n", binary)

	models, err := runtimellama.NewProvider(cfg.LlamaServer).Discover(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: Could not scan GGUF models: %v\n", err)
		return
	}
	if len(models) == 0 {
		fmt.Printf("  No GGUF models found yet. Add .gguf files to %s to use the managed runtime.\n", cfg.LlamaServer.ModelDirs[0])
		return
	}
	if strings.TrimSpace(cfg.LlamaServer.DefaultModel) == "" {
		if len(models) == 1 {
			cfg.LlamaServer.DefaultModel = models[0].Path
			fmt.Printf("  OK: Selected managed model %s\n", models[0].DisplayName)
		} else {
			fmt.Printf("  Found %d GGUF models. Set llama_server.default_model in config to choose one.\n", len(models))
		}
		return
	}
	fmt.Printf("  OK: Managed default model configured as %s\n", cfg.LlamaServer.DefaultModel)
}

func applyLlamaServerSetupDefaults(cfg *config.Config) {
	defaults := config.Defaults().LlamaServer
	if len(cfg.LlamaServer.ModelDirs) == 0 {
		cfg.LlamaServer.ModelDirs = defaults.ModelDirs
	}
	if cfg.LlamaServer.Host == "" {
		cfg.LlamaServer.Host = defaults.Host
	}
	if cfg.LlamaServer.ContextSize == 0 {
		cfg.LlamaServer.ContextSize = defaults.ContextSize
	}
	if cfg.LlamaServer.GPULayers == "" {
		cfg.LlamaServer.GPULayers = defaults.GPULayers
	}
	if cfg.LlamaServer.ReadinessTimeout == "" {
		cfg.LlamaServer.ReadinessTimeout = defaults.ReadinessTimeout
	}
	if cfg.LlamaServer.Restart.MaxAttempts == 0 {
		cfg.LlamaServer.Restart.MaxAttempts = defaults.Restart.MaxAttempts
	}
	if cfg.LlamaServer.Restart.Backoff == "" {
		cfg.LlamaServer.Restart.Backoff = defaults.Restart.Backoff
	}
}

func ensureLlamaModelDirs(dirs []string) error {
	for _, dir := range dirs {
		expanded, err := expandSetupPath(dir)
		if err != nil {
			return err
		}
		if expanded == "" {
			continue
		}
		if err := os.MkdirAll(expanded, 0755); err != nil {
			return err
		}
	}
	return nil
}

func findLlamaServerBinary(cfg config.LlamaServerConfig) (string, error) {
	if strings.TrimSpace(cfg.Binary) != "" {
		path, err := expandSetupPath(cfg.Binary)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("llama-server binary %s is a directory", path)
		}
		return path, nil
	}
	path, err := exec.LookPath("llama-server")
	if err != nil {
		return "", err
	}
	return path, nil
}

func expandSetupPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Abs(path)
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

func hasInstalledModel(installed []string, model string) bool {
	normalized := strings.TrimSuffix(model, ":latest")
	for _, candidate := range installed {
		if strings.TrimSuffix(candidate, ":latest") == normalized {
			return true
		}
	}
	return false
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

// runLogs implements the `cercano logs` subcommand. Today it only
// supports --crashes (inspect the crash log); a future flag can add
// other log surfaces without changing the subcommand name.
//
// Usage: cercano logs --crashes [--tail N]
//
//	--crashes: pretty-print the last N crash-log entries (default 10).
//	--tail N : override the count.
//
// Output is human-readable, one entry per block with a divider — meant
// for a person eyeballing the log. For structured consumption, the raw
// JSONL file at ~/.config/cercano/crash.log is the authoritative
// source.
func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	crashes := fs.Bool("crashes", false, "show the persistent crash log")
	tail := fs.Int("tail", 10, "number of most-recent entries to show")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if !*crashes {
		fmt.Fprintln(os.Stderr, "usage: cercano logs --crashes [--tail N]")
		os.Exit(2)
	}
	path := filepath.Join(filepath.Dir(config.DefaultPath()), "crash.log")
	entries, err := crashlog.TailEntries(path, *tail)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logs: read %s: %v\n", path, err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Printf("No crashes recorded at %s.\n", path)
		return
	}
	fmt.Printf("Crash log: %s (showing %d most-recent, newest-first)\n\n", path, len(entries))
	for i, e := range entries {
		if i > 0 {
			fmt.Println(strings.Repeat("─", 60))
		}
		fmt.Printf("[%s] %s\n", e.Timestamp.Local().Format("2006-01-02 15:04:05"), e.Kind)
		if e.CercanoVersion != "" {
			fmt.Printf("  cercano version: %s\n", e.CercanoVersion)
		}
		if e.Uptime > 0 {
			fmt.Printf("  uptime at crash: %s\n", e.Uptime.Round(time.Second))
		}
		if e.Signal != "" {
			fmt.Printf("  signal: %s\n", e.Signal)
		}
		if e.Reason != "" {
			fmt.Printf("  reason: %s\n", e.Reason)
		}
		for k, v := range e.Extra {
			fmt.Printf("  %s: %v\n", k, v)
		}
		if e.Stack != "" {
			fmt.Println("  stack:")
			for _, line := range strings.Split(strings.TrimRight(e.Stack, "\n"), "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
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
	// Persistent crash log. Sits alongside config.yaml so operators can
	// find it after the fact. Failure to open the log is non-fatal —
	// crash recording is nice-to-have; the server should still start.
	crashLogPath := filepath.Join(filepath.Dir(config.DefaultPath()), "crash.log")
	crashWriter, cwErr := crashlog.NewWriter(crashLogPath, version)
	if cwErr != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to open crash log at %s: %v (server continues without crash capture)\n", crashLogPath, cwErr)
	}
	// Top-level panic recovery: catches anything the gRPC-handler
	// recovery interceptors missed (background goroutine panics that
	// propagated up, main-goroutine panics during startup, etc.). We
	// log then re-panic so the process still dies — the point is
	// observability, not resurrection.
	defer func() {
		if r := recover(); r != nil {
			if crashWriter != nil {
				crashWriter.LogPanic(fmt.Sprintf("%v", r), []byte(runtimeStack(true)), map[string]any{
					"stage": "runServerMode",
				})
				_ = crashWriter.Close()
			}
			panic(r)
		}
	}()

	fmt.Printf("Starting Cercano gRPC server (v%s)...\n", version)
	fmt.Printf("Local model: %s\n", cfg.OpenChatModel())
	fmt.Printf("Ollama URL: %s\n", cfg.OllamaURL)
	if crashWriter != nil {
		fmt.Printf("Crash log: %s\n", crashLogPath)
	}

	addr, cleanup, err := startGRPCServer(cfg, ":"+cfg.Port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n[ERROR] %v\n", err)
		if crashWriter != nil {
			crashWriter.LogPanic("server startup failed: "+err.Error(), nil, map[string]any{"stage": "startGRPCServer"})
			_ = crashWriter.Close()
		}
		os.Exit(1)
	}

	fmt.Printf("Server listening at %s\n", addr)

	// Serve until signaled. The dev launcher SIGTERMs stale agents on every
	// rebuild; dying instantly here severed every in-flight stream (clients
	// saw "Unavailable: error reading from server: EOF"), so drain instead.
	//
	// Every signal now also gets recorded to the crash log so operators
	// can distinguish a graceful stop from a mysterious disappearance.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	sig := <-sigCh
	fmt.Printf("Received %v — draining in-flight requests (signal again to force quit)...\n", sig)
	if crashWriter != nil {
		crashWriter.LogSignal(sig.String(), map[string]any{"stage": "runServerMode", "graceful": true})
	}
	go func() {
		<-sigCh
		if crashWriter != nil {
			crashWriter.LogSignal("second-signal-force-quit", nil)
			_ = crashWriter.Close()
		}
		fmt.Fprintln(os.Stderr, "Second signal — exiting immediately")
		os.Exit(1)
	}()
	cleanup()
	if crashWriter != nil {
		_ = crashWriter.Close()
	}
	fmt.Println("Shutdown complete")
}

// runtimeStack returns a stack trace suitable for crash-log capture.
// When all=true it includes every live goroutine (useful for OOM /
// deadlock investigations); when false it's just the calling goroutine.
func runtimeStack(all bool) string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, all)
	return string(buf[:n])
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
		fmt.Fprintf(os.Stderr, "Local model: %s | Ollama: %s\n", cfg.OpenChatModel(), cfg.OllamaURL)

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

// runWorkerMode serves the Worker gRPC service over a Unix-domain socket until
// killed. The host launches this process with --socket <path> and connects to
// it; with per-conversation worker reuse the host drives many sequential
// RunTurn streams over one connection to a warm worker.
func runWorkerMode(args []string) {
	var socketPath string
	for i, arg := range args {
		if arg == "--socket" && i+1 < len(args) {
			socketPath = args[i+1]
		}
	}
	if socketPath == "" {
		fmt.Fprintln(os.Stderr, "worker: --socket <path> required")
		os.Exit(1)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: listen %s: %v\n", socketPath, err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	proto.RegisterWorkerServer(srv, worker.New())

	log.Printf("[worker] serving on unix:%s", socketPath)
	if err := srv.Serve(ln); err != nil {
		fmt.Fprintf(os.Stderr, "worker: serve: %v\n", err)
		os.Exit(1)
	}
}
