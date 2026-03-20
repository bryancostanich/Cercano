package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	modelsResp     *proto.ListModelsResponse
	modelsErr      error
}

func (m *mockAgentClient) ProcessRequest(ctx context.Context, in *proto.ProcessRequestRequest, opts ...grpc.CallOption) (*proto.ProcessRequestResponse, error) {
	m.lastRequest = in
	return m.processResp, m.processErr
}

func (m *mockAgentClient) StreamProcessRequest(ctx context.Context, in *proto.ProcessRequestRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[proto.StreamProcessResponse], error) {
	return nil, nil
}

func (m *mockAgentClient) ListModels(ctx context.Context, in *proto.ListModelsRequest, opts ...grpc.CallOption) (*proto.ListModelsResponse, error) {
	if m.modelsResp != nil {
		return m.modelsResp, m.modelsErr
	}
	return &proto.ListModelsResponse{}, m.modelsErr
}

func (m *mockAgentClient) UpdateConfig(ctx context.Context, in *proto.UpdateConfigRequest, opts ...grpc.CallOption) (*proto.UpdateConfigResponse, error) {
	m.lastConfigReq = in
	if m.configResp != nil {
		return m.configResp, m.configErr
	}
	return &proto.UpdateConfigResponse{Success: true, Message: "Configuration updated"}, m.configErr
}

func (m *mockAgentClient) ListSkills(ctx context.Context, in *proto.ListSkillsRequest, opts ...grpc.CallOption) (*proto.ListSkillsResponse, error) {
	return &proto.ListSkillsResponse{}, nil
}

func (m *mockAgentClient) GetSkill(ctx context.Context, in *proto.GetSkillRequest, opts ...grpc.CallOption) (*proto.GetSkillResponse, error) {
	return &proto.GetSkillResponse{}, nil
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

func TestCercanoModels_ListModels(t *testing.T) {
	mock := &mockAgentClient{
		modelsResp: &proto.ListModelsResponse{
			Models: []*proto.ModelInfo{
				{Name: "qwen3-coder:latest", Size: 4700000000, ModifiedAt: "2026-03-15T10:30:00Z"},
				{Name: "llama3:latest", Size: 8100000000, ModifiedAt: "2026-03-14T09:00:00Z"},
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
		Name:      "cercano_models",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if text == "" {
		t.Error("expected non-empty response")
	}
	// Should contain model names
	if !contains(text, "qwen3-coder") {
		t.Errorf("expected response to contain 'qwen3-coder', got %q", text)
	}
	if !contains(text, "llama3") {
		t.Errorf("expected response to contain 'llama3', got %q", text)
	}
}

func TestNewServer_RegistersModelsTool(t *testing.T) {
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
		if tool.Name == "cercano_models" {
			found = true
		}
	}
	if !found {
		t.Error("expected cercano_models tool to be registered")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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

// --- cercano_classify tests ---

func TestNewServer_RegistersClassifyTool(t *testing.T) {
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
		if tool.Name == "cercano_classify" {
			found = true
		}
	}
	if !found {
		t.Error("expected cercano_classify tool to be registered")
	}
}

func TestCercanoClassify_Basic(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "Category: bug\nConfidence: high\nReasoning: The stack trace indicates a null pointer dereference.",
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
		Name: "cercano_classify",
		Arguments: map[string]any{
			"text":       "panic: runtime error: invalid memory address or nil pointer dereference",
			"categories": "bug, config issue, infra problem",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if !strings.Contains(mock.lastRequest.Input, "Classify the following") {
		t.Errorf("expected classify instruction in prompt, got %q", mock.lastRequest.Input)
	}
	if !strings.Contains(mock.lastRequest.Input, "bug, config issue, infra problem") {
		t.Errorf("expected categories in prompt, got %q", mock.lastRequest.Input)
	}
	if mock.lastRequest.WorkDir != "" {
		t.Errorf("expected empty work_dir, got %q", mock.lastRequest.WorkDir)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "Category: bug") {
		t.Errorf("expected classification output, got %q", text)
	}
}

func TestCercanoClassify_NoCategories(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "Category: error log\nConfidence: high\nReasoning: Contains error output.",
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
		Name: "cercano_classify",
		Arguments: map[string]any{
			"text": "ERROR: connection refused",
		},
	})

	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if !strings.Contains(mock.lastRequest.Input, "Determine the most appropriate category") {
		t.Errorf("expected default category instruction when none provided, got %q", mock.lastRequest.Input)
	}
}

func TestCercanoClassify_MissingText(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "cercano_classify",
		Arguments: map[string]any{},
	})
	if err != nil {
		return
	}
	if result != nil && result.IsError {
		return
	}
	t.Error("expected error when text is missing")
}

func TestCercanoClassify_GRPCError(t *testing.T) {
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
		Name: "cercano_classify",
		Arguments: map[string]any{
			"text": "some text",
		},
	})
	if err != nil {
		return
	}
	if result == nil {
		t.Fatal("expected either an error or a result")
	}
	if !result.IsError {
		t.Error("expected IsError=true when gRPC call fails")
	}
}

// --- cercano_explain tests ---

func TestNewServer_RegistersExplainTool(t *testing.T) {
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
		if tool.Name == "cercano_explain" {
			found = true
		}
	}
	if !found {
		t.Error("expected cercano_explain tool to be registered")
	}
}

func TestCercanoExplain_WithText(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "This function sorts a slice of integers using bubble sort.",
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
		Name: "cercano_explain",
		Arguments: map[string]any{
			"text": "func sort(a []int) { for i := range a { for j := i+1; j < len(a); j++ { if a[j] < a[i] { a[i], a[j] = a[j], a[i] } } } }",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if !strings.Contains(mock.lastRequest.Input, "Explain the following") {
		t.Errorf("expected explain instruction in prompt, got %q", mock.lastRequest.Input)
	}
	if !strings.Contains(mock.lastRequest.Input, "func sort") {
		t.Errorf("expected code in prompt, got %q", mock.lastRequest.Input)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if text != "This function sorts a slice of integers using bubble sort." {
		t.Errorf("expected clean output, got %q", text)
	}
}

func TestCercanoExplain_WithFilePath(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")
	os.WriteFile(testFile, []byte("package main\n\nfunc hello() string { return \"world\" }\n"), 0644)

	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{Output: "A simple function that returns the string 'world'."},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	_, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_explain",
		Arguments: map[string]any{
			"file_path": testFile,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if !strings.Contains(mock.lastRequest.Input, "func hello()") {
		t.Errorf("expected file content in prompt, got %q", mock.lastRequest.Input)
	}
}

func TestCercanoExplain_NoInput(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "cercano_explain",
		Arguments: map[string]any{},
	})
	if err != nil {
		return
	}
	if result != nil && result.IsError {
		return
	}
	t.Error("expected error when neither text nor file_path provided")
}

func TestCercanoExplain_GRPCError(t *testing.T) {
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
		Name: "cercano_explain",
		Arguments: map[string]any{
			"text": "some code",
		},
	})
	if err != nil {
		return
	}
	if result == nil {
		t.Fatal("expected either an error or a result")
	}
	if !result.IsError {
		t.Error("expected IsError=true when gRPC call fails")
	}
}

// --- cercano_summarize tests ---

func TestNewServer_RegistersSummarizeTool(t *testing.T) {
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
		if tool.Name == "cercano_summarize" {
			found = true
		}
	}
	if !found {
		t.Error("expected cercano_summarize tool to be registered")
	}
}

func TestCercanoSummarize_WithText(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "This is a concise summary.",
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
		Name: "cercano_summarize",
		Arguments: map[string]any{
			"text": "A very long document with lots of content that needs summarizing.",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	// Verify the gRPC request contains summarize prompt.
	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if !strings.Contains(mock.lastRequest.Input, "Summarize the following") {
		t.Errorf("expected summarize instruction in prompt, got %q", mock.lastRequest.Input)
	}
	if !strings.Contains(mock.lastRequest.Input, "A very long document") {
		t.Errorf("expected user text in prompt, got %q", mock.lastRequest.Input)
	}
	// Should not pass WorkDir/FileName (stateless tool).
	if mock.lastRequest.WorkDir != "" {
		t.Errorf("expected empty work_dir, got %q", mock.lastRequest.WorkDir)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if text != "This is a concise summary." {
		t.Errorf("expected clean output without routing metadata, got %q", text)
	}
}

func TestCercanoSummarize_WithMaxLength(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{Output: "Brief."},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_summarize",
		Arguments: map[string]any{
			"text":       "Some content.",
			"max_length": "brief",
		},
	})

	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if !strings.Contains(mock.lastRequest.Input, "1-2 sentences") {
		t.Errorf("expected 'brief' to map to '1-2 sentences' in prompt, got %q", mock.lastRequest.Input)
	}
}

func TestCercanoSummarize_WithFilePath(t *testing.T) {
	// Create a temp file with known content.
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")
	os.WriteFile(testFile, []byte("package main\n\nfunc main() {}\n"), 0644)

	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{Output: "A Go main package."},
	}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	_, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_summarize",
		Arguments: map[string]any{
			"file_path": testFile,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if !strings.Contains(mock.lastRequest.Input, "package main") {
		t.Errorf("expected file content in prompt, got %q", mock.lastRequest.Input)
	}
}

func TestCercanoSummarize_NoInput(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "cercano_summarize",
		Arguments: map[string]any{},
	})
	if err != nil {
		return // error propagated as Go error
	}
	if result != nil && result.IsError {
		return // error in result
	}
	t.Error("expected error when neither text nor file_path provided")
}

func TestCercanoSummarize_FileNotFound(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_summarize",
		Arguments: map[string]any{
			"file_path": "/nonexistent/file.txt",
		},
	})
	if err != nil {
		return // error propagated
	}
	if result != nil && result.IsError {
		return // error in result
	}
	t.Error("expected error for nonexistent file")
}

func TestCercanoSummarize_GRPCError(t *testing.T) {
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
		Name: "cercano_summarize",
		Arguments: map[string]any{
			"text": "some text",
		},
	})
	if err != nil {
		return // error propagated
	}
	if result == nil {
		t.Fatal("expected either an error or a result")
	}
	if !result.IsError {
		t.Error("expected IsError=true when gRPC call fails")
	}
}

// --- cercano_extract tests ---

func TestNewServer_RegistersExtractTool(t *testing.T) {
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
		if tool.Name == "cercano_extract" {
			found = true
		}
	}
	if !found {
		t.Error("expected cercano_extract tool to be registered")
	}
}

func TestCercanoExtract_Basic(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output: "Error: connection timeout on line 42",
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
		Name: "cercano_extract",
		Arguments: map[string]any{
			"text":  "Line 1: OK\nLine 42: Error: connection timeout\nLine 43: OK",
			"query": "error messages",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastRequest == nil {
		t.Fatal("expected gRPC request to be made")
	}
	if !strings.Contains(mock.lastRequest.Input, "error messages") {
		t.Errorf("expected query in prompt, got %q", mock.lastRequest.Input)
	}
	if !strings.Contains(mock.lastRequest.Input, "Line 42: Error") {
		t.Errorf("expected source text in prompt, got %q", mock.lastRequest.Input)
	}
	if mock.lastRequest.WorkDir != "" {
		t.Errorf("expected empty work_dir, got %q", mock.lastRequest.WorkDir)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if text != "Error: connection timeout on line 42" {
		t.Errorf("expected clean output, got %q", text)
	}
}

func TestCercanoExtract_MissingText(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_extract",
		Arguments: map[string]any{
			"query": "errors",
		},
	})
	if err != nil {
		return
	}
	if result != nil && result.IsError {
		return
	}
	t.Error("expected error when text is missing")
}

func TestCercanoExtract_MissingQuery(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_extract",
		Arguments: map[string]any{
			"text": "some log content",
		},
	})
	if err != nil {
		return
	}
	if result != nil && result.IsError {
		return
	}
	t.Error("expected error when query is missing")
}

func TestCercanoExtract_GRPCError(t *testing.T) {
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
		Name: "cercano_extract",
		Arguments: map[string]any{
			"text":  "some text",
			"query": "errors",
		},
	})
	if err != nil {
		return
	}
	if result == nil {
		t.Fatal("expected either an error or a result")
	}
	if !result.IsError {
		t.Error("expected IsError=true when gRPC call fails")
	}
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
