package mcp

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/pkg/proto"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// formatGRPCError wraps gRPC errors with actionable diagnostic messages.
func formatGRPCError(err error, operation string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("%s: connection refused. Is the Cercano gRPC server running? Start it with: cd source/server && make agent && bin/agent", operation)
	case strings.Contains(msg, "unavailable"):
		return fmt.Errorf("%s: server unavailable. The Cercano gRPC server may not be running or may be starting up", operation)
	case strings.Contains(msg, "Ollama") || strings.Contains(msg, "ollama"):
		return fmt.Errorf("%s: Ollama error. Is Ollama running? Start it with: ollama serve", operation)
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
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

// ModelsRequest is the input schema for the cercano_models tool.
type ModelsRequest struct{}

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
