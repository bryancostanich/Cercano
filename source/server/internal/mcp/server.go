package mcp

import (
	"context"
	"fmt"
	"os"

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

// registerTools registers all Cercano MCP tools with the server.
func (s *Server) registerTools() {
	// Tools will be registered in Phase 2.
	fmt.Fprintln(os.Stderr, "Cercano MCP server initialized (no tools registered yet)")
}

// ChatRequest is the input schema for the cercano_chat tool.
type ChatRequest struct {
	Message        string `json:"message" jsonschema:"The question or discussion prompt"`
	Context        string `json:"context,omitempty" jsonschema:"Code or file contents for reference"`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Conversation ID for multi-turn support"`
}

// GenerateRequest is the input schema for the cercano_generate tool.
type GenerateRequest struct {
	Instruction    string `json:"instruction" jsonschema:"What to generate or modify"`
	FilePath       string `json:"file_path,omitempty" jsonschema:"Target file path for code changes"`
	WorkDir        string `json:"work_dir,omitempty" jsonschema:"Working directory for validation"`
	Context        string `json:"context,omitempty" jsonschema:"Existing code or file contents for context"`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Conversation ID for multi-turn support"`
}

// ReviewRequest is the input schema for the cercano_review tool.
type ReviewRequest struct {
	Code         string `json:"code" jsonschema:"The code to review"`
	Instructions string `json:"instructions,omitempty" jsonschema:"Specific review criteria or focus areas"`
	FilePath     string `json:"file_path,omitempty" jsonschema:"File path for context"`
}

// SummarizeRequest is the input schema for the cercano_summarize tool.
type SummarizeRequest struct {
	Content string `json:"content" jsonschema:"The content to summarize"`
	Format  string `json:"format,omitempty" jsonschema:"Desired output format (e.g. bullet points or one paragraph)"`
}

// ClassifyRequest is the input schema for the cercano_classify tool.
type ClassifyRequest struct {
	Query string `json:"query" jsonschema:"The task description to classify"`
}

// ConfigRequest is the input schema for the cercano_config tool.
type ConfigRequest struct {
	Action        string `json:"action" jsonschema:"get or set"`
	LocalModel    string `json:"local_model,omitempty" jsonschema:"Local model name to set"`
	CloudProvider string `json:"cloud_provider,omitempty" jsonschema:"Cloud provider to set (google or anthropic)"`
	CloudModel    string `json:"cloud_model,omitempty" jsonschema:"Cloud model to set"`
}

// handleChat processes a cercano_chat tool call.
func (s *Server) handleChat(ctx context.Context, request *gomcp.CallToolRequest, args ChatRequest) (*gomcp.CallToolResult, any, error) {
	input := args.Message
	if args.Context != "" {
		input = fmt.Sprintf("%s\n\nContext:\n%s", args.Message, args.Context)
	}

	resp, err := s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:          input,
		ConversationId: args.ConversationID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("gRPC call failed: %w", err)
	}

	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: resp.Output},
		},
	}, nil, nil
}

// handleGenerate processes a cercano_generate tool call.
func (s *Server) handleGenerate(ctx context.Context, request *gomcp.CallToolRequest, args GenerateRequest) (*gomcp.CallToolResult, any, error) {
	input := args.Instruction
	if args.Context != "" {
		input = fmt.Sprintf("%s\n\nExisting code:\n%s", args.Instruction, args.Context)
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
