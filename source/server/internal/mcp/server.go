package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"

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

// Server wraps the MCP server and its gRPC client connection to the Cercano agent.
type Server struct {
	mcpServer  *gomcp.Server
	grpcClient proto.AgentClient
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
	}

	s.registerTools()

	return s
}

// MCPServer returns the underlying MCP server for transport binding.
func (s *Server) MCPServer() *gomcp.Server {
	return s.mcpServer
}

// LocalRequest is the input schema for the cercano_local tool.
type LocalRequest struct {
	Prompt         string `json:"prompt" jsonschema:"The prompt to run against local models"`
	FilePath       string `json:"file_path,omitempty" jsonschema:"Target file path for code changes. When provided with work_dir, enables the agentic code generation loop with validation."`
	WorkDir        string `json:"work_dir,omitempty" jsonschema:"Working directory for code validation (go build/test). When provided with file_path, enables the agentic code generation loop."`
	Context        string `json:"context,omitempty" jsonschema:"Additional context such as existing code or file contents"`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Conversation ID for multi-turn support across calls"`
}

// ConfigRequest is the input schema for the cercano_config tool.
type ConfigRequest struct {
	Action        string `json:"action" jsonschema:"get or set"`
	LocalModel    string `json:"local_model,omitempty" jsonschema:"Local model name to set"`
	CloudProvider string `json:"cloud_provider,omitempty" jsonschema:"Cloud provider to set (google or anthropic)"`
	CloudModel    string `json:"cloud_model,omitempty" jsonschema:"Cloud model to set"`
	OllamaURL     string `json:"ollama_url,omitempty" jsonschema:"Ollama endpoint URL (e.g. http://mac-studio.local:11434)"`
}

// SummarizeRequest is the input schema for the cercano_summarize tool.
type SummarizeRequest struct {
	Text      string `json:"text,omitempty" jsonschema:"Raw text to summarize. Provide either text or file_path."`
	FilePath  string `json:"file_path,omitempty" jsonschema:"Path to a file to read and summarize. Provide either text or file_path."`
	MaxLength string `json:"max_length,omitempty" jsonschema:"Target summary length: brief (1-2 sentences), medium (1 paragraph, default), or detailed (multiple paragraphs)."`
}

// ExtractRequest is the input schema for the cercano_extract tool.
type ExtractRequest struct {
	Text  string `json:"text" jsonschema:"The text to search through and extract information from"`
	Query string `json:"query" jsonschema:"What to find or extract (e.g. 'error messages', 'function signatures', 'config values')"`
}

// ClassifyRequest is the input schema for the cercano_classify tool.
type ClassifyRequest struct {
	Text       string `json:"text" jsonschema:"The text to classify or triage"`
	Categories string `json:"categories,omitempty" jsonschema:"Comma-separated list of categories to choose from. If omitted, the model will determine appropriate categories."`
}

// ExplainRequest is the input schema for the cercano_explain tool.
type ExplainRequest struct {
	Text     string `json:"text,omitempty" jsonschema:"Code or text to explain. Provide either text or file_path."`
	FilePath string `json:"file_path,omitempty" jsonschema:"Path to a file to read and explain. Provide either text or file_path."`
}

// ModelsRequest is the input schema for the cercano_models tool.
type ModelsRequest struct{}

// SkillsRequest is the input schema for the cercano_skills tool.
type SkillsRequest struct {
	Action string `json:"action" jsonschema:"list to get all skills, or get to retrieve a specific skill"`
	Name   string `json:"name,omitempty" jsonschema:"Skill name to retrieve (required when action is get)"`
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
		Description: "Query or update Cercano's runtime configuration. Use action 'set' to change the local model, Ollama endpoint URL, cloud provider, or cloud model without restarting the server.",
	}, s.handleConfig)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_summarize",
		Description: "Summarize text or a file using local AI. Returns a concise summary without sending the full content to the cloud. Use this to distill large files, logs, diffs, or documents before processing.",
	}, s.handleSummarize)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_extract",
		Description: "Extract specific information from text using local AI. Returns only the relevant sections matching your query. Use this to pull function signatures, error messages, config values, or other targeted info from large text without sending everything to the cloud.",
	}, s.handleExtract)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_classify",
		Description: "Classify or triage text using local AI. Returns a category, confidence level, and brief reasoning. Use this for quick local triage of errors, logs, code quality, or any content that needs categorization without sending it to the cloud.",
	}, s.handleClassify)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_explain",
		Description: "Explain code or text using local AI. Returns a clear explanation of what the code does, its key interfaces, and data flow. Use this to understand unfamiliar code locally before deciding what context to send to the cloud.",
	}, s.handleExplain)

	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_skills",
		Description: "List or retrieve Cercano's Agent Skills. Use action 'list' to get a catalog of all available skills with descriptions. Use action 'get' with a skill name to retrieve the full SKILL.md definition.",
	}, s.handleSkills)
}

// handleLocal processes a cercano_local tool call.
func (s *Server) handleLocal(ctx context.Context, request *gomcp.CallToolRequest, args LocalRequest) (*gomcp.CallToolResult, any, error) {
	input := args.Prompt
	if args.Context != "" {
		input = fmt.Sprintf("%s\n\nContext:\n%s", args.Prompt, args.Context)
	}

	resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:          input,
		WorkDir:        args.WorkDir,
		FileName:       args.FilePath,
		ConversationId: args.ConversationID,
	})
	if err != nil {
		return nil, nil, formatGRPCError(err, "cercano_local")
	}

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

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output},
		},
	}, nil, nil
}

// handleModels processes a cercano_models tool call.
func (s *Server) handleModels(ctx context.Context, request *gomcp.CallToolRequest, args ModelsRequest) (*gomcp.CallToolResult, any, error) {
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
	switch args.Action {
	case "set":
		resp, err := s.grpcClient.UpdateConfig(ctx, &proto.UpdateConfigRequest{
			LocalModel:    args.LocalModel,
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
		return nil, nil, fmt.Errorf("invalid action %q: must be \"set\"", args.Action)
	}
}

// handleSummarize processes a cercano_summarize tool call.
func (s *Server) handleSummarize(ctx context.Context, request *gomcp.CallToolRequest, args SummarizeRequest) (*gomcp.CallToolResult, any, error) {
	if args.Text == "" && args.FilePath == "" {
		return nil, nil, fmt.Errorf("cercano_summarize: provide either 'text' or 'file_path'")
	}

	content := args.Text
	if args.FilePath != "" {
		data, err := os.ReadFile(args.FilePath)
		if err != nil {
			return nil, nil, fmt.Errorf("cercano_summarize: failed to read file %q: %w", args.FilePath, err)
		}
		content = string(data)
	}

	lengthInstruction := "one paragraph"
	switch args.MaxLength {
	case "brief":
		lengthInstruction = "1-2 sentences"
	case "detailed":
		lengthInstruction = "multiple paragraphs covering all key points"
	}

	prompt := fmt.Sprintf("Summarize the following text in %s. Focus on the most important information. Output only the summary, no preamble.\n\nText to summarize:\n%s", lengthInstruction, content)

	resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:       prompt,
		DirectLocal: true,
	})
	if err != nil {
		return nil, nil, formatGRPCError(err, "cercano_summarize")
	}

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: resp.Output},
		},
	}, nil, nil
}

// handleExtract processes a cercano_extract tool call.
func (s *Server) handleExtract(ctx context.Context, request *gomcp.CallToolRequest, args ExtractRequest) (*gomcp.CallToolResult, any, error) {
	if args.Text == "" {
		return nil, nil, fmt.Errorf("cercano_extract: 'text' is required")
	}
	if args.Query == "" {
		return nil, nil, fmt.Errorf("cercano_extract: 'query' is required")
	}

	prompt := fmt.Sprintf("Extract the following from the text below: %s\n\nRules:\n- Output ONLY the extracted content, no commentary\n- Preserve the original formatting of extracted sections\n- If nothing matches, respond with \"No matching content found.\"\n\nText:\n%s", args.Query, args.Text)

	resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:       prompt,
		DirectLocal: true,
	})
	if err != nil {
		return nil, nil, formatGRPCError(err, "cercano_extract")
	}

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: resp.Output},
		},
	}, nil, nil
}

// handleClassify processes a cercano_classify tool call.
func (s *Server) handleClassify(ctx context.Context, request *gomcp.CallToolRequest, args ClassifyRequest) (*gomcp.CallToolResult, any, error) {
	if args.Text == "" {
		return nil, nil, fmt.Errorf("cercano_classify: 'text' is required")
	}

	categoryInstruction := "Determine the most appropriate category."
	if args.Categories != "" {
		categoryInstruction = fmt.Sprintf("Choose from these categories: %s", args.Categories)
	}

	prompt := fmt.Sprintf("Classify the following text. %s\n\nRespond with exactly this format:\nCategory: <category>\nConfidence: <high/medium/low>\nReasoning: <one sentence explanation>\n\nText:\n%s", categoryInstruction, args.Text)

	resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:       prompt,
		DirectLocal: true,
	})
	if err != nil {
		return nil, nil, formatGRPCError(err, "cercano_classify")
	}

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: resp.Output},
		},
	}, nil, nil
}

// handleExplain processes a cercano_explain tool call.
func (s *Server) handleExplain(ctx context.Context, request *gomcp.CallToolRequest, args ExplainRequest) (*gomcp.CallToolResult, any, error) {
	if args.Text == "" && args.FilePath == "" {
		return nil, nil, fmt.Errorf("cercano_explain: provide either 'text' or 'file_path'")
	}

	content := args.Text
	if args.FilePath != "" {
		data, err := os.ReadFile(args.FilePath)
		if err != nil {
			return nil, nil, fmt.Errorf("cercano_explain: failed to read file %q: %w", args.FilePath, err)
		}
		content = string(data)
	}

	prompt := fmt.Sprintf("Explain the following code or text. Describe what it does, its key components, and how they interact. Be concise and focus on what a developer needs to understand to work with this code.\n\nCode:\n%s", content)

	resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:       prompt,
		DirectLocal: true,
	})
	if err != nil {
		return nil, nil, formatGRPCError(err, "cercano_explain")
	}

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: resp.Output},
		},
	}, nil, nil
}

// handleSkills processes a cercano_skills tool call.
func (s *Server) handleSkills(ctx context.Context, request *gomcp.CallToolRequest, args SkillsRequest) (*gomcp.CallToolResult, any, error) {
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
