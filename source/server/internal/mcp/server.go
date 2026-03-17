package mcp

import (
	"context"
	"fmt"

	"cercano/source/server/pkg/proto"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
}

// registerTools registers all Cercano MCP tools with the server.
func (s *Server) registerTools() {
	gomcp.AddTool(s.mcpServer, &gomcp.Tool{
		Name:        "cercano_local",
		Description: "Run a prompt against Cercano's local AI models (Ollama). Handles both chat-style queries and code generation. When file_path and work_dir are provided, uses an agentic generate-validate loop with automatic self-correction. Otherwise, processes the prompt as a direct LLM call. Use this to offload work to local inference — faster, private, and at zero cost.",
	}, s.handleLocal)
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
		return nil, nil, fmt.Errorf("gRPC call failed: %w", err)
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
		output += fmt.Sprintf("\n\n[Model: %s, Confidence: %.2f, Escalated: %v]",
			resp.RoutingMetadata.ModelName, resp.RoutingMetadata.Confidence, resp.RoutingMetadata.Escalated)
	}

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output},
		},
	}, nil, nil
}
