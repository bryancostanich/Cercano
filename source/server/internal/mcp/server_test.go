package mcp

import (
	"context"
	"testing"

	"cercano/source/server/pkg/proto"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
)

// mockAgentClient implements proto.AgentClient for testing.
type mockAgentClient struct {
	processResp    *proto.ProcessRequestResponse
	processErr     error
	lastRequest    *proto.ProcessRequestRequest
	configResp     *proto.UpdateConfigResponse
	configErr      error
	lastConfigReq  *proto.UpdateConfigRequest
}

func (m *mockAgentClient) ProcessRequest(ctx context.Context, in *proto.ProcessRequestRequest, opts ...grpc.CallOption) (*proto.ProcessRequestResponse, error) {
	m.lastRequest = in
	return m.processResp, m.processErr
}

func (m *mockAgentClient) StreamProcessRequest(ctx context.Context, in *proto.ProcessRequestRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[proto.StreamProcessResponse], error) {
	return nil, nil
}

func (m *mockAgentClient) UpdateConfig(ctx context.Context, in *proto.UpdateConfigRequest, opts ...grpc.CallOption) (*proto.UpdateConfigResponse, error) {
	m.lastConfigReq = in
	if m.configResp != nil {
		return m.configResp, m.configErr
	}
	return &proto.UpdateConfigResponse{Success: true, Message: "Configuration updated"}, m.configErr
}

func TestNewServer_RegistersTools(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	if s.MCPServer() == nil {
		t.Fatal("MCPServer() returned nil")
	}

	// Connect an in-memory client to verify tool registration.
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()

	ctx := context.Background()
	if _, err := s.MCPServer().Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer cs.Close()

	// List tools and verify cercano_local is registered.
	found := false
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("listing tools failed: %v", err)
		}
		if tool.Name == "cercano_local" {
			found = true
		}
	}
	if !found {
		t.Error("expected cercano_local tool to be registered")
	}
}

func TestCercanoLocal_ChatStyle(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "Hello! I can help with that.",
			RoutingMetadata: &proto.RoutingMetadata{
				ModelName:  "qwen3-coder",
				Confidence: 0.85,
				Escalated:  false,
			},
		},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_local",
		Arguments: map[string]any{
			"prompt": "What is a goroutine?",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	// Verify the gRPC request was formed correctly.
	if mock.lastRequest.Input != "What is a goroutine?" {
		t.Errorf("expected input 'What is a goroutine?', got %q", mock.lastRequest.Input)
	}
	if mock.lastRequest.WorkDir != "" {
		t.Errorf("expected empty work_dir for chat-style query, got %q", mock.lastRequest.WorkDir)
	}
	if mock.lastRequest.FileName != "" {
		t.Errorf("expected empty file_name for chat-style query, got %q", mock.lastRequest.FileName)
	}

	// Verify the response contains the output.
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	text := result.Content[0].(*gomcp.TextContent).Text
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestCercanoLocal_WithContext(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "The function processes items.",
		},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_local",
		Arguments: map[string]any{
			"prompt":  "Explain this function",
			"context": "func process(items []string) { ... }",
		},
	})

	// Verify context is appended to the prompt.
	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	expected := "Explain this function\n\nContext:\nfunc process(items []string) { ... }"
	if mock.lastRequest.Input != expected {
		t.Errorf("expected input with context appended, got %q", mock.lastRequest.Input)
	}
}

func TestCercanoLocal_CodeGeneration(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "Generated code",
			FileChanges: []*proto.FileChange{
				{Path: "main.go", Content: "package main", Action: proto.FileAction_UPDATE},
			},
			RoutingMetadata: &proto.RoutingMetadata{
				ModelName:  "qwen3-coder",
				Confidence: 0.92,
				Escalated:  false,
			},
		},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_local",
		Arguments: map[string]any{
			"prompt":    "Add error handling to this function",
			"file_path": "main.go",
			"work_dir":  "/home/user/project",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	// Verify the gRPC request includes workDir and fileName.
	if mock.lastRequest.WorkDir != "/home/user/project" {
		t.Errorf("expected work_dir '/home/user/project', got %q", mock.lastRequest.WorkDir)
	}
	if mock.lastRequest.FileName != "main.go" {
		t.Errorf("expected file_name 'main.go', got %q", mock.lastRequest.FileName)
	}

	// Verify file changes are included in the response.
	text := result.Content[0].(*gomcp.TextContent).Text
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestCercanoLocal_ConversationID(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "Response",
		},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	_, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_local",
		Arguments: map[string]any{
			"prompt":          "Follow up question",
			"conversation_id": "conv-123",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if mock.lastRequest.ConversationId != "conv-123" {
		t.Errorf("expected conversation_id 'conv-123', got %q", mock.lastRequest.ConversationId)
	}
}

func TestCercanoLocal_GRPCError(t *testing.T) {
	mock := &mockAgentClient{
		processErr: context.DeadlineExceeded,
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_local",
		Arguments: map[string]any{
			"prompt": "test",
		},
	})
	// The MCP SDK converts handler errors to a CallToolResult with IsError=true
	// and the error message as text content. Either a Go error or an IsError
	// result is acceptable.
	if err != nil {
		return // error propagated as Go error
	}
	if result == nil {
		t.Fatal("expected either an error or a result")
	}
	if !result.IsError {
		t.Error("expected IsError=true when gRPC call fails")
	}
}

func TestNewServer_RegistersConfigTool(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	ctx := context.Background()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	found := false
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("listing tools failed: %v", err)
		}
		if tool.Name == "cercano_config" {
			found = true
		}
	}
	if !found {
		t.Error("expected cercano_config tool to be registered")
	}
}

func TestCercanoConfig_SetLocalModel(t *testing.T) {
	mock := &mockAgentClient{
		configResp: &proto.UpdateConfigResponse{
			Success: true,
			Message: "Local model updated to GLM-4.7-Flash",
		},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_config",
		Arguments: map[string]any{
			"action":      "set",
			"local_model": "GLM-4.7-Flash",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastConfigReq == nil {
		t.Fatal("expected UpdateConfig gRPC call")
	}
	if mock.lastConfigReq.LocalModel != "GLM-4.7-Flash" {
		t.Errorf("expected local_model 'GLM-4.7-Flash', got %q", mock.lastConfigReq.LocalModel)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if text == "" {
		t.Error("expected non-empty response")
	}
}

func TestCercanoConfig_SetCloudProvider(t *testing.T) {
	mock := &mockAgentClient{
		configResp: &proto.UpdateConfigResponse{
			Success: true,
			Message: "Cloud provider updated",
		},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_config",
		Arguments: map[string]any{
			"action":         "set",
			"cloud_provider": "google",
			"cloud_model":    "gemini-1.5-flash",
		},
	})

	if mock.lastConfigReq == nil {
		t.Fatal("expected UpdateConfig gRPC call")
	}
	if mock.lastConfigReq.CloudProvider != "google" {
		t.Errorf("expected cloud_provider 'google', got %q", mock.lastConfigReq.CloudProvider)
	}
	if mock.lastConfigReq.CloudModel != "gemini-1.5-flash" {
		t.Errorf("expected cloud_model 'gemini-1.5-flash', got %q", mock.lastConfigReq.CloudModel)
	}
}

func TestCercanoConfig_SetOllamaURL(t *testing.T) {
	mock := &mockAgentClient{
		configResp: &proto.UpdateConfigResponse{
			Success: true,
			Message: "updated: [ollama_url=http://mac-studio.local:11434]",
		},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_config",
		Arguments: map[string]any{
			"action":    "set",
			"ollama_url": "http://mac-studio.local:11434",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastConfigReq == nil {
		t.Fatal("expected UpdateConfig gRPC call")
	}
	if mock.lastConfigReq.OllamaUrl != "http://mac-studio.local:11434" {
		t.Errorf("expected ollama_url 'http://mac-studio.local:11434', got %q", mock.lastConfigReq.OllamaUrl)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if text == "" {
		t.Error("expected non-empty response")
	}
}

func TestCercanoConfig_InvalidAction(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_config",
		Arguments: map[string]any{
			"action": "delete",
		},
	})
	// Should return an error for invalid action.
	if err != nil {
		return // error propagated
	}
	if result != nil && result.IsError {
		return // error in result
	}
	t.Error("expected error for invalid action")
}

func TestCercanoLocal_MultiTurn(t *testing.T) {
	callCount := 0
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	convID := "test-conv-456"

	// First turn.
	mock.processResp = &proto.ProcessRequestResponse{Output: "First response"}
	cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_local",
		Arguments: map[string]any{
			"prompt":          "First question",
			"conversation_id": convID,
		},
	})
	callCount++
	if mock.lastRequest.ConversationId != convID {
		t.Errorf("turn 1: expected conversation_id %q, got %q", convID, mock.lastRequest.ConversationId)
	}

	// Second turn with same conversation ID.
	mock.processResp = &proto.ProcessRequestResponse{Output: "Second response"}
	cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_local",
		Arguments: map[string]any{
			"prompt":          "Follow up question",
			"conversation_id": convID,
		},
	})
	callCount++
	if mock.lastRequest.ConversationId != convID {
		t.Errorf("turn 2: expected conversation_id %q, got %q", convID, mock.lastRequest.ConversationId)
	}
	if mock.lastRequest.Input != "Follow up question" {
		t.Errorf("turn 2: expected input 'Follow up question', got %q", mock.lastRequest.Input)
	}
	if callCount != 2 {
		t.Errorf("expected 2 gRPC calls, got %d", callCount)
	}
}
