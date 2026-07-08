package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/mcpadapter"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/document"
	"cercano/source/server/internal/research"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/tokens"
	"cercano/source/server/internal/web"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// formatGRPCError wraps gRPC errors with actionable diagnostic hints
// while preserving the original error message for debugging.
func formatGRPCError(err error, operation string) error {
	msg := err.Error()
	var hint string
	switch {
	case strings.Contains(msg, "connection refused"):
		hint = " (hint: Is the Cercano gRPC server running? Start it with: cd source/server && make agent && bin/agent)"
	case strings.Contains(msg, "unavailable"):
		hint = " (hint: The Cercano gRPC server may not be running or may be starting up)"
	case strings.Contains(msg, "Ollama") || strings.Contains(msg, "ollama"):
		hint = " (hint: Is Ollama running? Start it with: ollama serve)"
	}
	return fmt.Errorf("%s: %s%s", operation, msg, hint)
}

// CoprocMeta is the structured routing metadata returned alongside a
// co-processor tool's text result. Clients (CLI, host agents) decide how to
// surface it; Tier is "local" or "cloud".
type CoprocMeta struct {
	Model  string `json:"model"`
	Tier   string `json:"tier"`
	Notice string `json:"notice,omitempty"`
}

func coprocMeta(resp *proto.ProcessRequestResponse) CoprocMeta {
	tier := "local"
	if resp.GetRoutingMetadata().GetIsCloud() {
		tier = "cloud"
	}
	return CoprocMeta{
		Model:  resp.GetRoutingMetadata().GetModelName(),
		Tier:   tier,
		Notice: resp.GetNotice(),
	}
}

// Server wraps the MCP server and its gRPC client connection to the Cercano agent.
type Server struct {
	mcpServer       *gomcp.Server
	grpcClient      proto.AgentClient
	startupErr      string               // non-empty when the server started in degraded mode
	collector       *telemetry.Collector // optional; nil disables telemetry
	ctxLoader       *projectctx.Loader   // project context loader
	updateVersion   string               // latest available version, empty if up to date
	updateCommand   string               // upgrade command to show the user
	updateNudgeSent bool                 // true after the first tool response nudge
}

// NewServer creates a new MCP server backed by the given gRPC client.
func NewServer(grpcClient proto.AgentClient) *Server {
	mcpServer := gomcp.NewServer(
		&gomcp.Implementation{Name: "cercano", Version: "0.1.0"},
		nil,
	)

	s := &Server{
		mcpServer:  mcpServer,
		grpcClient: grpcClient,
		ctxLoader:  projectctx.NewLoader(),
	}

	s.registerTools()

	return s
}

// NewDegradedServer creates an MCP server that registers all tools but returns
// a startup error for every call. This keeps the MCP stdio pipe alive so the
// client receives a clear diagnostic instead of "Failed to reconnect".
func NewDegradedServer(startupErr error) *Server {
	mcpServer := gomcp.NewServer(
		&gomcp.Implementation{Name: "cercano", Version: "0.1.0"},
		nil,
	)

	s := &Server{
		mcpServer:  mcpServer,
		startupErr: startupErr.Error(),
		ctxLoader:  projectctx.NewLoader(),
	}

	s.registerTools()

	return s
}

// checkDegraded returns a tool error result if the server started in degraded
// mode. Callers should return immediately when ok is true.
func (s *Server) checkDegraded() (result *gomcp.CallToolResult, ok bool) {
	if s.startupErr == "" {
		return nil, false
	}
	return &gomcp.CallToolResult{
		IsError: true,
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: fmt.Sprintf("Cercano is not available: %s", s.startupErr)},
		},
	}, true
}

// SetCollector attaches a telemetry collector for usage tracking.
func (s *Server) SetCollector(c *telemetry.Collector) {
	s.collector = c
}

// SetUpdateInfo stores update information for the session nudge.
func (s *Server) SetUpdateInfo(latestVersion, upgradeCommand string) {
	s.updateVersion = latestVersion
	s.updateCommand = upgradeCommand
}

// maybeUpdateNudge appends an update notification to the first tool response in a session.
func (s *Server) maybeUpdateNudge(result *gomcp.CallToolResult) *gomcp.CallToolResult {
	if s.updateNudgeSent || s.updateVersion == "" {
		return result
	}
	s.updateNudgeSent = true
	nudge := fmt.Sprintf("\n\n---\n*Note: Cercano v%s is available. Run `%s` to update.*", s.updateVersion, s.updateCommand)
	if len(result.Content) > 0 {
		if tc, ok := result.Content[0].(*gomcp.TextContent); ok {
			tc.Text += nudge
		}
	}
	return result
}

// notifyProgress sends an MCP progress notification if the request has a progress token.
// Errors are silently ignored — progress is best-effort.
func notifyProgress(ctx context.Context, req *gomcp.CallToolRequest, message string, progress, total float64) {
	if req == nil || req.Params == nil {
		return
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return
	}
	if req.Session == nil {
		return
	}
	req.Session.NotifyProgress(ctx, &gomcp.ProgressNotificationParams{
		ProgressToken: token,
		Message:       message,
		Progress:      progress,
		Total:         total,
	})
}

// emitEvent is a helper that emits a telemetry event if a collector is configured.
// tokenSaving indicates whether this call substitutes for a cloud call (counts toward savings).
// cloudTokens optionally records host-reported cloud token usage alongside this event.
// contentTokensAvoided is the estimated cloud tokens saved by handling content locally.
func (s *Server) emitEvent(toolName string, resp *proto.ProcessRequestResponse, startTime int64, tokenSaving bool, cloudTokens *cloudTokenFields, contentTokensAvoided int) {
	if s.collector == nil {
		return
	}
	model := ""
	wasEscalated := false
	cloudProvider := ""
	if resp != nil && resp.RoutingMetadata != nil {
		model = resp.RoutingMetadata.ModelName
		wasEscalated = resp.RoutingMetadata.Escalated
	}
	inputTokens := 0
	outputTokens := 0
	if resp != nil {
		inputTokens = int(resp.InputTokens)
		outputTokens = int(resp.OutputTokens)
	}
	e := &telemetry.Event{
		Timestamp:            time.Unix(0, startTime),
		ToolName:             toolName,
		Model:                model,
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		DurationMs:           time.Since(time.Unix(0, startTime)).Milliseconds(),
		WasEscalated:         wasEscalated,
		CloudProvider:        cloudProvider,
		TokenSaving:          tokenSaving,
		ContentTokensAvoided: contentTokensAvoided,
	}
	s.collector.Emit(e)

	// Record host-reported cloud usage if provided
	if cloudTokens != nil && (cloudTokens.HostCloudTokensIn > 0 || cloudTokens.HostCloudTokensOut > 0) {
		s.collector.EmitCloudUsage(telemetry.CloudUsageReport{
			Timestamp:         time.Now(),
			CloudInputTokens:  cloudTokens.HostCloudTokensIn,
			CloudOutputTokens: cloudTokens.HostCloudTokensOut,
		})
	}
}

// withContext prepends project context to a prompt if available.
func (s *Server) withContext(projectDir, prompt string) string {
	return s.ctxLoader.PrependContext(projectDir, prompt)
}

// nudgeMessage is appended to tool responses when the project hasn't been initialized.
const nudgeMessage = "\n\n---\n*Note: Cercano hasn't been initialized for this project. Running `cercano_init` with the project directory will enable project-aware responses. Recommended if you'll use Cercano more than once in this session.*"

// maybeNudge appends an init recommendation to the result if the project isn't initialized,
// and an update nudge on the first tool response if an update is available.
func (s *Server) maybeNudge(projectDir string, result *gomcp.CallToolResult) *gomcp.CallToolResult {
	if projectDir != "" && s.ctxLoader.NudgeNeeded(projectDir) {
		if len(result.Content) > 0 {
			if tc, ok := result.Content[0].(*gomcp.TextContent); ok {
				tc.Text += nudgeMessage
			}
		}
	}
	return s.maybeUpdateNudge(result)
}

// venvMissingMessage is returned when cercano_research is called without the Python venv.
const venvMissingMessage = "Web research requires a Python virtual environment that is not set up. Run `cercano setup` to create it automatically."

// venvNudgeMessage is appended to cercano_init output when the venv is missing.
const venvNudgeMessage = "\n\n---\n*Note: The Python venv for web research is not set up. Run `cercano setup` to enable `cercano_research` (DuckDuckGo search + local model analysis).*"

// isVenvReady returns true if the Python venv exists and has ddgs installed.
func isVenvReady() bool {
	pythonPath := config.VenvPython()
	if _, err := os.Stat(pythonPath); err != nil {
		return false
	}
	return true
}

// MCPServer returns the underlying MCP server for transport binding.
func (s *Server) MCPServer() *gomcp.Server {
	return s.mcpServer
}

// cloudTokenFields are optional fields for host-reported cloud token usage.
// Included in all co-processor tool requests to enable automatic tracking.
type cloudTokenFields struct {
	HostCloudTokensIn  int `json:"host_cloud_tokens_in,omitempty" jsonschema:"Your cloud input tokens since the last cercano call. Include this to help track cloud vs local usage."`
	HostCloudTokensOut int `json:"host_cloud_tokens_out,omitempty" jsonschema:"Your cloud output tokens since the last cercano call. Include this to help track cloud vs local usage."`
}

// OpenRequest is the input schema for the cercano_local tool.
type OpenRequest struct {
	Prompt         string `json:"prompt" jsonschema:"The prompt to run against local models"`
	FilePath       string `json:"file_path,omitempty" jsonschema:"Target file path for code changes. When provided with work_dir, enables the agentic code generation loop with validation."`
	WorkDir        string `json:"work_dir,omitempty" jsonschema:"Working directory for code validation (go build/test). When provided with file_path, enables the agentic code generation loop."`
	Context        string `json:"context,omitempty" jsonschema:"Additional context such as existing code or file contents"`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Conversation ID for multi-turn support across calls"`
	cloudTokenFields
}

// ConfigRequest is the input schema for the cercano_config tool.
type ConfigRequest struct {
	Action        string `json:"action" jsonschema:"get (list available Ollama models) or set (change configuration)"`
	OpenRuntime  string `json:"local_runtime,omitempty" jsonschema:"Local runtime to use for generation (ollama or llama_server)"`
	OpenModel    string `json:"local_model,omitempty" jsonschema:"Local model name to set (use action 'get' to see available models)"`
	CloudProvider string `json:"cloud_provider,omitempty" jsonschema:"Cloud provider to set (google or anthropic)"`
	CloudModel    string `json:"cloud_model,omitempty" jsonschema:"Cloud model to set"`
	OllamaURL     string `json:"ollama_url,omitempty" jsonschema:"Ollama endpoint URL (e.g. http://mac-studio.local:11434)"`
}

// ModelsRequest is the input schema for the cercano_models tool.
type ModelsRequest struct{}

// SkillsRequest is the input schema for the cercano_skills tool.
type SkillsRequest struct {
	Action string `json:"action" jsonschema:"list to get all skills, or get to retrieve a specific skill"`
	Name   string `json:"name,omitempty" jsonschema:"Skill name to retrieve (required when action is get)"`
}

// FetchRequest is the input schema for the cercano_fetch tool.
type FetchRequest struct {
	URL        string `json:"url" jsonschema:"URL to fetch and extract text from."`
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Project root directory. Enables project-aware responses when .cercano/context.md exists."`
	cloudTokenFields
}

// ResearchRequest is the input schema for the cercano_research tool.
type ResearchRequest struct {
	Query      string `json:"query" jsonschema:"The research question to investigate via web search and local model analysis."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"Maximum number of pages to fetch and analyze (default 5)."`
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Project root directory. Enables project-aware responses when .cercano/context.md exists."`
	cloudTokenFields
}

// InitRequest is the input schema for the cercano_init tool.
type InitRequest struct {
	ProjectDir string `json:"project_dir" jsonschema:"Project root directory to scan and build context for (required)."`
	Context    string `json:"context,omitempty" jsonschema:"Optional domain knowledge you already have about this project. Only provide what you already know — do not research the project to fill this in. Cercano will scan the repo itself."`
}

// StatsRequest is the input schema for the cercano_stats tool.
type StatsRequest struct{}

// SubmitUsageRequest is the input schema for the cercano_submit_usage tool.
type SubmitUsageRequest struct {
	CloudInputTokens  int    `json:"cloud_input_tokens" jsonschema:"Number of tokens sent to the cloud model"`
	CloudOutputTokens int    `json:"cloud_output_tokens" jsonschema:"Number of tokens received from the cloud model"`
	CloudProvider     string `json:"cloud_provider,omitempty" jsonschema:"Cloud provider name (e.g. anthropic, google)"`
	CloudModel        string `json:"cloud_model,omitempty" jsonschema:"Cloud model name (e.g. claude-opus-4-6, gemini-3-flash)"`
}

// DocumentRequest is the input schema for the cercano_document tool.
type DocumentRequest struct {
	FilePath   string `json:"file_path" jsonschema:"Path to the Go source file to document."`
	Style      string `json:"style,omitempty" jsonschema:"Doc comment style: minimal (1-2 sentences, default) or detailed (multi-line with params)."`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"If true, report what would be documented but do not write changes."`
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Project root directory. Enables project-aware responses when .cercano/context.md exists."`
	cloudTokenFields
}

// DeepResearchRequest is the input schema for the cercano_deep_research tool.
type DeepResearchRequest struct {
	Topic      string   `json:"topic" jsonschema:"The research topic to investigate."`
	Intent     string   `json:"intent" jsonschema:"What you need this research for — drives relevance scoring and source selection."`
	Depth      string   `json:"depth,omitempty" jsonschema:"Research depth: survey (quick landscape scan, ~2 min), standard (balanced, ~5-8 min), or deep (exhaustive with reference chasing, ~15+ min). Default: standard."`
	DateRange  string   `json:"date_range,omitempty" jsonschema:"Filter results by date range (e.g. '2024-2026', 'last 2 years', 'after 2023-06')."`
	Sources    []string `json:"sources,omitempty" jsonschema:"Override auto-detected sources. If omitted, sources are chosen based on topic domain."`
	OutputDir  string   `json:"output_dir,omitempty" jsonschema:"Write the report to this directory as multiple files (README.md, findings/, references/, synthesis.md). Recommended for thorough research."`
	ProjectDir string   `json:"project_dir,omitempty" jsonschema:"Project root directory."`
	Phase      string   `json:"phase,omitempty" jsonschema:"Run a specific phase: plan (select sources), search (find results), analyze (assess findings), synthesize (write report). Omit to run all phases. Each phase shows results and suggests the next step."`
	UseModel   string   `json:"use_model,omitempty" jsonschema:"Use this model for the research run instead of the default."`
	cloudTokenFields
}

// registerTools registers all Cercano MCP tools with the server.
func (s *Server) registerTools() {
	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_local",
		Description: "Run a prompt against Cercano's local AI models (Ollama). Handles both chat-style queries and code generation. When file_path and work_dir are provided, uses an agentic generate-validate loop with automatic self-correction. Otherwise, processes the prompt as a direct LLM call. Use this to offload work to local inference — faster, private, and at zero cost.",
	}, s.handleLocal)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_models",
		Description: "List models available on the active Ollama instance. Returns model names, sizes, and modification dates. Useful for discovering what models are available on a remote machine before switching.",
	}, s.handleModels)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_config",
		Description: "Query or update Cercano's runtime configuration. Use action 'get' to list available local models from Ollama. Use action 'set' to change the local model, Ollama endpoint URL, cloud provider, or cloud model without restarting the server.",
	}, s.handleConfig)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_skills",
		Description: "List or retrieve Cercano's Agent Skills. Use action 'list' to get a catalog of all available skills with descriptions. Use action 'get' with a skill name to retrieve the full SKILL.md definition.",
	}, s.handleSkills)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_submit_usage",
		Description: "Submit cloud token usage data to Cercano (opt-in). This is for sending data, not viewing it — use cercano_stats to see usage reports. Helps Cercano track cloud tokens alongside local inference for accurate local-vs-cloud comparison.",
	}, s.handleSubmitUsage)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_stats",
		Description: "View Cercano usage statistics and cloud token savings. Shows total requests, tokens processed locally, cloud tokens reported by the host, percentage kept local, and breakdowns by tool, model, and day.",
	}, s.handleStats)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_fetch",
		Description: "Fetch a URL and extract readable text content. Returns the full extracted text (HTML stripped to plain text) — not a summary. Use this to read web pages, documentation, articles, or any URL locally without sending the content to the cloud.",
	}, s.handleFetch)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_research",
		Description: "Research a question using web search and local AI analysis. Crafts search queries, searches DuckDuckGo, fetches top results, and synthesizes a sourced answer — all locally. Use this instead of browsing the web yourself to save cloud context tokens. Requires Python venv (run 'cercano setup' first).",
	}, s.handleResearch)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_init",
		Description: "Initialize Cercano for a project. Scans the repo to build a project context file (.cercano/context.md) that makes all Cercano tools project-aware. Optionally accepts domain knowledge the host AI already has. Do NOT research the project to populate the context parameter — only provide knowledge you already have. Cercano will scan the repo itself.",
	}, s.handleInit)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_document",
		Description: "Generate doc comments for exported Go symbols using local AI and write them directly to the file. The host never sees the file contents — Cercano handles the entire read-think-write cycle locally. Returns only a summary of what was documented. Supports dry_run mode to preview without writing.",
	}, s.handleDocument)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_deep_research",
		Description: "Deep multi-source research tool. Takes a topic and intent, identifies authoritative sources (academic, industry, news, reference), systematically searches each one, analyzes and ranks findings by relevance and impact, chases cited references, and compiles a structured report with executive summary, contradiction detection, gap analysis, and follow-up suggestions. The entire pipeline runs locally. Use output_dir for thorough research — writes findings as individual files.",
	}, s.handleDeepResearch)

	// Register all mcp-surface capabilities as cercano_<name> tools that
	// forward to the agent via InvokeCapability. Requires builtins to be
	// imported (directly or transitively) for MCPCatalog to return non-nil.
	mcpadapter.RegisterCapabilities(s.mcpServer, s.grpcClient, capabilities.MCPCatalog())
}

// handleLocal processes a cercano_local tool call.
func (s *Server) handleLocal(ctx context.Context, request *gomcp.CallToolRequest, args OpenRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	startTime := time.Now().UnixNano()
	input := args.Prompt
	if args.Context != "" {
		input = fmt.Sprintf("%s\n\nContext:\n%s", args.Prompt, args.Context)
	}
	input = s.withContext(args.WorkDir, input)

	resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:          input,
		WorkDir:        args.WorkDir,
		FileName:       args.FilePath,
		ConversationId: args.ConversationID,
	})
	if err != nil {
		return nil, nil, formatGRPCError(err, "cercano_local")
	}
	s.emitEvent("cercano_local", resp, startTime, true, &args.cloudTokenFields, tokens.Estimate(input))

	output := resp.Output
	if len(resp.FileChanges) > 0 {
		output += "\n\nFile Changes:\n"
		for _, fc := range resp.FileChanges {
			output += fmt.Sprintf("- %s %s\n", fc.Action, fc.Path)
			if fc.Content != "" {
				output += fmt.Sprintf("```\n%s\n```\n", fc.Content)
			}
		}
	}
	if resp.ValidationErrors != "" {
		output += fmt.Sprintf("\nValidation Errors:\n%s", resp.ValidationErrors)
	}
	if resp.RoutingMetadata != nil {
		endpointInfo := resp.RoutingMetadata.Endpoint
		if resp.RoutingMetadata.IsFallback {
			endpointInfo += " (fallback)"
		}
		output += fmt.Sprintf("\n\n[Model: %s, Confidence: %.2f, Escalated: %v, Endpoint: %s]",
			resp.RoutingMetadata.ModelName, resp.RoutingMetadata.Confidence, resp.RoutingMetadata.Escalated, endpointInfo)
	}

	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output},
		},
	}
	return s.maybeNudge(args.WorkDir, result), nil, nil
}

// handleModels processes a cercano_models tool call.
func (s *Server) handleModels(ctx context.Context, request *gomcp.CallToolRequest, args ModelsRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	resp, err := s.grpcClient.ListModels(ctx, &proto.ListModelsRequest{})
	if err != nil {
		return nil, nil, formatGRPCError(err, "cercano_models")
	}

	if len(resp.Models) == 0 {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: "No models found on the active Ollama instance."},
			},
		}, nil, nil
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Available models (%d):\n\n", len(resp.Models)))
	for _, m := range resp.Models {
		sizeMB := float64(m.Size) / 1_000_000
		sizeStr := fmt.Sprintf("%.0f MB", sizeMB)
		if sizeMB >= 1000 {
			sizeStr = fmt.Sprintf("%.1f GB", sizeMB/1000)
		}
		output.WriteString(fmt.Sprintf("- %s (%s, modified: %s)\n", m.Name, sizeStr, m.ModifiedAt))
	}

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output.String()},
		},
	}, nil, nil
}

// handleConfig processes a cercano_config tool call.
func (s *Server) handleConfig(ctx context.Context, request *gomcp.CallToolRequest, args ConfigRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	switch args.Action {
	case "get":
		modelsResp, err := s.grpcClient.ListModels(ctx, &proto.ListModelsRequest{})
		if err != nil {
			return nil, nil, formatGRPCError(err, "cercano_config")
		}

		var output strings.Builder
		output.WriteString("Available local models (from active local runtime):\n\n")
		if len(modelsResp.Models) == 0 {
			output.WriteString("  (no models installed)\n")
		}
		for _, m := range modelsResp.Models {
			sizeMB := float64(m.Size) / 1_000_000
			sizeStr := fmt.Sprintf("%.0f MB", sizeMB)
			if sizeMB >= 1000 {
				sizeStr = fmt.Sprintf("%.1f GB", sizeMB/1000)
			}
			output.WriteString(fmt.Sprintf("- %s (%s)\n", m.Name, sizeStr))
		}
		output.WriteString("\nUse action 'set' with local_model to switch models.")

		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: output.String()},
			},
		}, nil, nil

	case "set":
		resp, err := s.grpcClient.UpdateConfig(ctx, &proto.UpdateConfigRequest{
			OpenRuntime:  args.OpenRuntime,
			OpenModel:    args.OpenModel,
			CloudProvider: args.CloudProvider,
			CloudModel:    args.CloudModel,
			OllamaUrl:     args.OllamaURL,
		})
		if err != nil {
			return nil, nil, formatGRPCError(err, "cercano_config")
		}

		status := "success"
		if !resp.Success {
			status = "failed"
		}
		output := fmt.Sprintf("Configuration update %s: %s", status, resp.Message)

		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: output},
			},
		}, nil, nil

	default:
		return nil, nil, fmt.Errorf("invalid action %q: must be \"get\" or \"set\"", args.Action)
	}
}

// handleSkills processes a cercano_skills tool call.
func (s *Server) handleSkills(ctx context.Context, request *gomcp.CallToolRequest, args SkillsRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	switch args.Action {
	case "list":
		resp, err := s.grpcClient.ListSkills(ctx, &proto.ListSkillsRequest{})
		if err != nil {
			return nil, nil, formatGRPCError(err, "cercano_skills")
		}

		var output string
		for _, skill := range resp.Skills {
			output += fmt.Sprintf("**%s** — %s\n\n", skill.Name, skill.Description)
		}
		if output == "" {
			output = "No skills available."
		}

		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: output},
			},
		}, nil, nil

	case "get":
		resp, err := s.grpcClient.GetSkill(ctx, &proto.GetSkillRequest{Name: args.Name})
		if err != nil {
			return nil, nil, formatGRPCError(err, "cercano_skills")
		}

		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: resp.Content},
			},
		}, nil, nil

	default:
		return nil, nil, fmt.Errorf("invalid action %q: must be 'list' or 'get'", args.Action)
	}
}

// handleSubmitUsage processes a cercano_submit_usage tool call.
func (s *Server) handleSubmitUsage(ctx context.Context, request *gomcp.CallToolRequest, args SubmitUsageRequest) (*gomcp.CallToolResult, any, error) {
	if s.collector == nil {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: "Telemetry is not enabled."},
			},
		}, nil, nil
	}

	s.collector.EmitCloudUsage(telemetry.CloudUsageReport{
		Timestamp:         time.Now(),
		CloudInputTokens:  args.CloudInputTokens,
		CloudOutputTokens: args.CloudOutputTokens,
		CloudProvider:     args.CloudProvider,
		CloudModel:        args.CloudModel,
	})

	total := args.CloudInputTokens + args.CloudOutputTokens
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: fmt.Sprintf("Recorded %d cloud tokens (%d in, %d out).", total, args.CloudInputTokens, args.CloudOutputTokens)},
		},
	}, nil, nil
}

// handleStats processes a cercano_stats tool call.
func (s *Server) handleStats(ctx context.Context, request *gomcp.CallToolRequest, args StatsRequest) (*gomcp.CallToolResult, any, error) {
	if s.collector == nil {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: "Telemetry is not enabled."},
			},
		}, nil, nil
	}

	stats, err := s.collector.Store().GetStats(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_stats: %w", err)
	}

	formatted := telemetry.FormatStatsASCII(stats)

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: formatted},
		},
	}, nil, nil
}

// handleFetch processes a cercano_fetch tool call.
func (s *Server) handleFetch(ctx context.Context, request *gomcp.CallToolRequest, args FetchRequest) (*gomcp.CallToolResult, any, error) {
	if args.URL == "" {
		return nil, nil, fmt.Errorf("cercano_fetch: 'url' is required")
	}

	fetcher := web.NewFetcher()
	fetchResult, err := fetcher.Fetch(args.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_fetch: %w", err)
	}

	output := fetchResult.Content
	if output == "" {
		output = "(No readable text content found at this URL)"
	}

	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output},
		},
	}
	return s.maybeNudge(args.ProjectDir, result), nil, nil
}

// grpcModelCaller adapts the gRPC client to the web.ModelCaller interface.
type grpcModelCaller struct {
	client proto.AgentClient
}

func (g *grpcModelCaller) Call(ctx context.Context, prompt string) (string, error) {
	resp, err := g.client.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:  prompt,
		Coproc: true,
	})
	if err != nil {
		return "", err
	}
	return resp.Output, nil
}

// grpcModelCallerWithTokens is like grpcModelCaller but accumulates token counts
// from multiple calls for telemetry reporting.
type grpcModelCallerWithTokens struct {
	client        proto.AgentClient
	modelOverride string // per-request model override
	lastResp      *proto.ProcessRequestResponse
	totalIn       int32
	totalOut      int32
	totalCalls    int
}

func (g *grpcModelCallerWithTokens) Call(ctx context.Context, prompt string) (string, error) {
	resp, err := g.client.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:         prompt,
		Coproc:        true,
		ModelOverride: g.modelOverride,
	})
	if err != nil {
		return "", err
	}
	g.lastResp = resp
	g.totalIn += int32(resp.InputTokens)
	g.totalOut += int32(resp.OutputTokens)
	g.totalCalls++
	return resp.Output, nil
}

// handleResearch processes a cercano_research tool call.
func (s *Server) handleResearch(ctx context.Context, request *gomcp.CallToolRequest, args ResearchRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	startTime := time.Now().UnixNano()

	if args.Query == "" {
		return nil, nil, fmt.Errorf("cercano_research: 'query' is required")
	}

	// Check venv
	if !isVenvReady() {
		return &gomcp.CallToolResult{
			IsError: true,
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: venvMissingMessage},
			},
		}, nil, nil
	}

	// Materialize the embedded search script
	scriptPath, err := web.EnsureSearchScript("")
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_research: search script: %w", err)
	}

	// Build pipeline dependencies
	modelCaller := &grpcModelCallerWithTokens{client: s.grpcClient}
	searcher := web.NewSearcher(config.VenvPython(), scriptPath)
	fetcher := web.NewFetcher()

	pipeline := web.NewResearchPipeline(modelCaller, searcher, fetcher)
	notifyProgress(ctx, request, "Researching locally...", 0, 2)
	result, err := pipeline.Run(ctx, s.withContext(args.ProjectDir, args.Query), args.MaxResults)
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_research: %w", err)
	}

	// Build output with sources
	output := result.Answer
	if len(result.Sources) > 0 {
		output += "\n\nSources:\n"
		for _, src := range result.Sources {
			output += fmt.Sprintf("- %s\n", src)
		}
	}

	// Emit telemetry — use the last gRPC response for routing metadata,
	// but report cumulative tokens
	resp := modelCaller.lastResp
	if resp != nil {
		resp.InputTokens = modelCaller.totalIn
		resp.OutputTokens = modelCaller.totalOut
	}
	s.emitEvent("cercano_research", resp, startTime, true, &args.cloudTokenFields, int(modelCaller.totalIn))
	notifyProgress(ctx, request, "Research complete", 2, 2)

	toolResult := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output},
		},
	}
	return s.maybeNudge(args.ProjectDir, toolResult), nil, nil
}

// handleInit processes a cercano_init tool call.
func (s *Server) handleInit(ctx context.Context, request *gomcp.CallToolRequest, args InitRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	startTime := time.Now().UnixNano()
	if args.ProjectDir == "" {
		return nil, nil, fmt.Errorf("cercano_init: project_dir is required")
	}

	// Scan the project
	scanner := projectctx.NewScanner()
	files, err := scanner.DiscoverFiles(args.ProjectDir)
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_init: failed to scan project: %w", err)
	}

	if len(files) == 0 {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: "No relevant files found in the project directory. Nothing to initialize."},
			},
		}, nil, nil
	}

	// Build the prompt for the local model
	builder := projectctx.NewBuilder()
	prompt, filesSummary := builder.BuildPrompt(files, args.Context)

	// Send to co-processor (locus-resolved tier)
	resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:  prompt,
		Coproc: true,
	})
	if err != nil {
		return nil, nil, formatGRPCError(err, "cercano_init")
	}
	s.emitEvent("cercano_init", resp, startTime, false, nil, 0)

	// Write the context file
	if err := builder.WriteContext(args.ProjectDir, resp.Output); err != nil {
		return nil, nil, fmt.Errorf("cercano_init: %w", err)
	}

	// Invalidate cache so next tool call picks up the new context
	s.ctxLoader.Invalidate(args.ProjectDir)

	output := fmt.Sprintf("Project initialized. %s\n\nContext written to %s (%d bytes).",
		filesSummary, projectctx.ContextPath(args.ProjectDir), len(resp.Output))

	// Nudge about venv if web research isn't available
	if !isVenvReady() {
		output += venvNudgeMessage
	}

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output},
		},
	}, nil, nil
}

// handleDocument processes a cercano_document tool call.
func (s *Server) handleDocument(ctx context.Context, request *gomcp.CallToolRequest, args DocumentRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	startTime := time.Now().UnixNano()

	if args.FilePath == "" {
		return nil, nil, fmt.Errorf("cercano_document: file_path is required")
	}

	// Determine style
	style := document.StyleMinimal
	if args.Style == "detailed" {
		style = document.StyleDetailed
	}

	// Parse the Go file
	symbols, err := document.ParseGoFile(args.FilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_document: %w", err)
	}

	undocumented := document.UndocumentedSymbols(symbols)
	if len(undocumented) == 0 {
		// Count documented symbols for the summary
		documented := 0
		for _, sym := range symbols {
			if sym.HasDoc {
				documented++
			}
		}
		result := &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: fmt.Sprintf("All %d exported symbols in %s are already documented.", documented, filepath.Base(args.FilePath))},
			},
		}
		return result, nil, nil
	}

	// Dry run: report what would be documented
	if args.DryRun {
		var sb strings.Builder
		fmt.Fprintf(&sb, "Dry run — would document %d of %d exported symbols in %s:\n", len(undocumented), len(symbols), filepath.Base(args.FilePath))
		for _, sym := range undocumented {
			label := string(sym.Kind)
			if sym.Receiver != "" {
				label = fmt.Sprintf("method on %s", sym.Receiver)
			}
			fmt.Fprintf(&sb, "  + %s (%s)\n", sym.Name, label)
		}
		// List already-documented symbols
		for _, sym := range symbols {
			if sym.HasDoc {
				fmt.Fprintf(&sb, "  . %s (already documented)\n", sym.Name)
			}
		}
		result := &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: sb.String()},
			},
		}
		return result, nil, nil
	}

	// Backup the file before making changes
	backupPath, err := document.BackupFile(args.FilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_document: backup failed: %w", err)
	}

	// Generate doc comments for each undocumented symbol
	var edits []document.DocEdit
	var documented []string
	var skipped []string
	var lastResp *proto.ProcessRequestResponse

	for _, sym := range undocumented {
		prompt := document.BuildPrompt(sym, style)
		prompt = s.withContext(args.ProjectDir, prompt)

		resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
			Input:  prompt,
			Coproc: true,
		})
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (inference error)", sym.Name))
			continue
		}
		lastResp = resp
		s.emitEvent("cercano_document", resp, startTime, true, &args.cloudTokenFields, tokens.Estimate(sym.Body)*2)

		comment := document.FormatAsGoDoc(resp.Output)
		if comment == "" {
			skipped = append(skipped, fmt.Sprintf("%s (empty response)", sym.Name))
			continue
		}

		edits = append(edits, document.DocEdit{
			Line:    sym.StartLine,
			Comment: comment,
		})

		label := string(sym.Kind)
		if sym.Receiver != "" {
			label = fmt.Sprintf("method on %s", sym.Receiver)
		}
		documented = append(documented, fmt.Sprintf("%s (%s)", sym.Name, label))
	}

	// Apply edits
	if len(edits) > 0 {
		if err := document.InsertDocComments(args.FilePath, edits); err != nil {
			// Restore from backup on failure
			document.RestoreFile(args.FilePath, backupPath)
			return nil, nil, fmt.Errorf("cercano_document: insert failed, file restored: %w", err)
		}
	}

	// Build summary
	var sb strings.Builder
	fmt.Fprintf(&sb, "Documented %d of %d exported symbols in %s:\n", len(documented), len(symbols), filepath.Base(args.FilePath))
	for _, d := range documented {
		fmt.Fprintf(&sb, "  + %s\n", d)
	}
	for _, sym := range symbols {
		if sym.HasDoc {
			fmt.Fprintf(&sb, "  . %s (already documented)\n", sym.Name)
		}
	}
	for _, s := range skipped {
		fmt.Fprintf(&sb, "  ! %s\n", s)
	}
	fmt.Fprintf(&sb, "\nBackup saved to %s", backupPath)

	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: sb.String()},
		},
	}
	return s.maybeNudge(args.ProjectDir, result), coprocMeta(lastResp), nil
}

// handleDeepResearch processes a cercano_deep_research tool call.
func (s *Server) handleDeepResearch(ctx context.Context, request *gomcp.CallToolRequest, args DeepResearchRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	startTime := time.Now().UnixNano()

	if args.Topic == "" {
		return nil, nil, fmt.Errorf("cercano_deep_research: 'topic' is required")
	}
	if args.Intent == "" {
		return nil, nil, fmt.Errorf("cercano_deep_research: 'intent' is required")
	}

	if !isVenvReady() {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{
				&gomcp.TextContent{Text: venvMissingMessage},
			},
		}, nil, nil
	}

	// Set up dependencies
	modelCaller := &grpcModelCallerWithTokens{client: s.grpcClient, modelOverride: args.UseModel}

	scriptPath, err := web.EnsureSearchScript("")
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_deep_research: search script: %w", err)
	}
	pythonPath := config.VenvPython()
	ddgSearcher := web.NewSearcher(pythonPath, scriptPath)

	// Adapt web.Searcher to research.SearchProvider
	searchAdapter := &webSearchAdapter{searcher: ddgSearcher}
	// Adapt web.Fetcher to research.URLFetcher
	fetchAdapter := &webFetchAdapter{fetcher: web.NewFetcher()}

	dispatcher := research.NewSearchDispatcher(searchAdapter)
	pipeline := research.NewPipeline(modelCaller, dispatcher, fetchAdapter)

	notifyProgress(ctx, request, "Starting deep research...", 0, 4)
	phaseResult, err := pipeline.Run(ctx, research.RunConfig{
		Topic:      args.Topic,
		Intent:     args.Intent,
		Depth:      args.Depth,
		DateRange:  args.DateRange,
		Sources:    args.Sources,
		OutputDir:  args.OutputDir,
		ProjectDir: args.ProjectDir,
		Phase:      args.Phase,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("cercano_deep_research: %w", err)
	}

	// Emit telemetry
	resp := modelCaller.lastResp
	if resp != nil {
		resp.InputTokens = modelCaller.totalIn
		resp.OutputTokens = modelCaller.totalOut
	}
	s.emitEvent("cercano_deep_research", resp, startTime, true, &args.cloudTokenFields, phaseResult.ContentTokensAvoided)
	notifyProgress(ctx, request, "Deep research complete", 4, 4)

	output := phaseResult.Summary

	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output},
		},
	}
	var metadata any
	if phaseResult.SuggestedNext != nil {
		metadata = map[string]any{
			"suggested_next": phaseResult.SuggestedNext,
		}
	}
	return s.maybeNudge(args.ProjectDir, result), metadata, nil
}

// webSearchAdapter adapts web.Searcher to research.SearchProvider.
type webSearchAdapter struct {
	searcher *web.Searcher
}

func (a *webSearchAdapter) Search(ctx context.Context, query string, maxResults int) ([]research.SearchResult, error) {
	results, err := a.searcher.Search(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}
	var out []research.SearchResult
	for _, r := range results {
		out = append(out, research.SearchResult{
			URL:     r.URL,
			Title:   r.Title,
			Snippet: r.Snippet,
		})
	}
	return out, nil
}

// webFetchAdapter adapts web.Fetcher to research.URLFetcher.
type webFetchAdapter struct {
	fetcher *web.Fetcher
}

func (a *webFetchAdapter) FetchURL(url string) (*research.FetchResult, error) {
	result, err := a.fetcher.Fetch(url)
	if err != nil {
		return nil, err
	}
	return &research.FetchResult{
		URL:     result.URL,
		Title:   result.Title,
		Content: result.Content,
	}, nil
}
