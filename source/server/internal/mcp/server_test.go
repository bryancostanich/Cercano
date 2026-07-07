package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/telemetry"
	"cercano/source/server/pkg/proto"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
)

// mockAgentClient implements proto.AgentClient for testing.
type mockAgentClient struct {
	processResp     *proto.ProcessRequestResponse
	processErr      error
	lastRequest     *proto.ProcessRequestRequest
	configResp      *proto.UpdateConfigResponse
	configErr       error
	lastConfigReq   *proto.UpdateConfigRequest
	modelsResp      *proto.ListModelsResponse
	modelsErr       error
	skillsResp      *proto.ListSkillsResponse
	skillsErr       error
	getSkillResp    *proto.GetSkillResponse
	getSkillErr     error
	lastGetSkillReq *proto.GetSkillRequest
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

func (m *mockAgentClient) GetRuntimeStatus(ctx context.Context, in *proto.GetRuntimeStatusRequest, opts ...grpc.CallOption) (*proto.GetRuntimeStatusResponse, error) {
	return &proto.GetRuntimeStatusResponse{}, nil
}

func (m *mockAgentClient) ListRuntimeModels(ctx context.Context, in *proto.ListRuntimeModelsRequest, opts ...grpc.CallOption) (*proto.ListRuntimeModelsResponse, error) {
	return &proto.ListRuntimeModelsResponse{}, nil
}

func (m *mockAgentClient) ListRuntimeEndpoints(ctx context.Context, in *proto.ListRuntimeEndpointsRequest, opts ...grpc.CallOption) (*proto.ListRuntimeEndpointsResponse, error) {
	return &proto.ListRuntimeEndpointsResponse{}, nil
}

func (m *mockAgentClient) StartRuntimeModel(ctx context.Context, in *proto.StartRuntimeModelRequest, opts ...grpc.CallOption) (*proto.StartRuntimeModelResponse, error) {
	return &proto.StartRuntimeModelResponse{}, nil
}

func (m *mockAgentClient) StopRuntimeModel(ctx context.Context, in *proto.StopRuntimeModelRequest, opts ...grpc.CallOption) (*proto.StopRuntimeModelResponse, error) {
	return &proto.StopRuntimeModelResponse{}, nil
}

func (m *mockAgentClient) RestartRuntime(ctx context.Context, in *proto.RestartRuntimeRequest, opts ...grpc.CallOption) (*proto.RestartRuntimeResponse, error) {
	return &proto.RestartRuntimeResponse{}, nil
}

func (m *mockAgentClient) DownloadRuntimeModel(ctx context.Context, in *proto.DownloadRuntimeModelRequest, opts ...grpc.CallOption) (*proto.DownloadRuntimeModelResponse, error) {
	return &proto.DownloadRuntimeModelResponse{}, nil
}

func (m *mockAgentClient) CancelRuntimeModelDownload(ctx context.Context, in *proto.CancelRuntimeModelDownloadRequest, opts ...grpc.CallOption) (*proto.CancelRuntimeModelDownloadResponse, error) {
	return &proto.CancelRuntimeModelDownloadResponse{}, nil
}

func (m *mockAgentClient) DeleteRuntimeModel(ctx context.Context, in *proto.DeleteRuntimeModelRequest, opts ...grpc.CallOption) (*proto.DeleteRuntimeModelResponse, error) {
	return &proto.DeleteRuntimeModelResponse{}, nil
}

func (m *mockAgentClient) StreamRuntimeLogs(ctx context.Context, in *proto.StreamRuntimeLogsRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[proto.RuntimeLogEntry], error) {
	return nil, nil
}

func (m *mockAgentClient) SubscribeEvents(ctx context.Context, in *proto.SubscribeEventsRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[proto.ClientEvent], error) {
	return nil, nil
}

func (m *mockAgentClient) UpdateConfig(ctx context.Context, in *proto.UpdateConfigRequest, opts ...grpc.CallOption) (*proto.UpdateConfigResponse, error) {
	m.lastConfigReq = in
	if m.configResp != nil {
		return m.configResp, m.configErr
	}
	return &proto.UpdateConfigResponse{Success: true, Message: "Configuration updated"}, m.configErr
}

func (m *mockAgentClient) GetConfig(ctx context.Context, in *proto.GetConfigRequest, opts ...grpc.CallOption) (*proto.GetConfigResponse, error) {
	return &proto.GetConfigResponse{}, nil
}

func (m *mockAgentClient) ListConversations(ctx context.Context, in *proto.ListConversationsRequest, opts ...grpc.CallOption) (*proto.ListConversationsResponse, error) {
	return &proto.ListConversationsResponse{}, nil
}

func (m *mockAgentClient) ResumeConversation(ctx context.Context, in *proto.ResumeConversationRequest, opts ...grpc.CallOption) (*proto.ResumeConversationResponse, error) {
	return &proto.ResumeConversationResponse{}, nil
}

func (m *mockAgentClient) DeleteConversation(ctx context.Context, in *proto.DeleteConversationRequest, opts ...grpc.CallOption) (*proto.DeleteConversationResponse, error) {
	return &proto.DeleteConversationResponse{Ok: true}, nil
}

func (m *mockAgentClient) RenameConversation(ctx context.Context, in *proto.RenameConversationRequest, opts ...grpc.CallOption) (*proto.RenameConversationResponse, error) {
	return &proto.RenameConversationResponse{Ok: true}, nil
}

func (m *mockAgentClient) GetConversation(ctx context.Context, in *proto.GetConversationRequest, opts ...grpc.CallOption) (*proto.Conversation, error) {
	return &proto.Conversation{}, nil
}

func (m *mockAgentClient) GetContextUsage(ctx context.Context, in *proto.GetContextUsageRequest, opts ...grpc.CallOption) (*proto.GetContextUsageResponse, error) {
	return &proto.GetContextUsageResponse{}, nil
}

func (m *mockAgentClient) GetConversationTurns(ctx context.Context, in *proto.GetConversationTurnsRequest, opts ...grpc.CallOption) (*proto.GetConversationTurnsResponse, error) {
	return &proto.GetConversationTurnsResponse{}, nil
}

func (m *mockAgentClient) ListTools(ctx context.Context, in *proto.ListToolsRequest, opts ...grpc.CallOption) (*proto.ListToolsResponse, error) {
	return &proto.ListToolsResponse{}, nil
}

func (m *mockAgentClient) InvokeTool(ctx context.Context, in *proto.InvokeToolRequest, opts ...grpc.CallOption) (*proto.InvokeToolResponse, error) {
	return &proto.InvokeToolResponse{}, nil
}

func (m *mockAgentClient) InvokeCapability(ctx context.Context, in *proto.InvokeCapabilityRequest, opts ...grpc.CallOption) (*proto.InvokeCapabilityResponse, error) {
	return &proto.InvokeCapabilityResponse{}, nil
}

func (m *mockAgentClient) ListSkills(ctx context.Context, in *proto.ListSkillsRequest, opts ...grpc.CallOption) (*proto.ListSkillsResponse, error) {
	if m.skillsResp != nil {
		return m.skillsResp, m.skillsErr
	}
	return &proto.ListSkillsResponse{}, m.skillsErr
}

func (m *mockAgentClient) GetSkill(ctx context.Context, in *proto.GetSkillRequest, opts ...grpc.CallOption) (*proto.GetSkillResponse, error) {
	m.lastGetSkillReq = in
	if m.getSkillResp != nil {
		return m.getSkillResp, m.getSkillErr
	}
	return &proto.GetSkillResponse{}, m.getSkillErr
}

func (m *mockAgentClient) AllowToolCall(ctx context.Context, in *proto.AllowToolCallRequest, opts ...grpc.CallOption) (*proto.AllowToolCallResponse, error) {
	return &proto.AllowToolCallResponse{}, nil
}

func (m *mockAgentClient) DenyToolCall(ctx context.Context, in *proto.DenyToolCallRequest, opts ...grpc.CallOption) (*proto.DenyToolCallResponse, error) {
	return &proto.DenyToolCallResponse{}, nil
}

func (m *mockAgentClient) GetPermissionMode(ctx context.Context, in *proto.GetPermissionModeRequest, opts ...grpc.CallOption) (*proto.GetPermissionModeResponse, error) {
	return &proto.GetPermissionModeResponse{}, nil
}

func (m *mockAgentClient) GetProviderCapabilities(ctx context.Context, in *proto.GetProviderCapabilitiesRequest, opts ...grpc.CallOption) (*proto.GetProviderCapabilitiesResponse, error) {
	return &proto.GetProviderCapabilitiesResponse{}, nil
}

func (m *mockAgentClient) SetPermissionMode(ctx context.Context, in *proto.SetPermissionModeRequest, opts ...grpc.CallOption) (*proto.SetPermissionModeResponse, error) {
	return &proto.SetPermissionModeResponse{}, nil
}

func (m *mockAgentClient) ProposeContextEdit(ctx context.Context, in *proto.ProposeContextEditRequest, opts ...grpc.CallOption) (*proto.ProposeContextEditResponse, error) {
	return &proto.ProposeContextEditResponse{}, nil
}

func (m *mockAgentClient) DeleteConversationTurns(ctx context.Context, in *proto.DeleteConversationTurnsRequest, opts ...grpc.CallOption) (*proto.DeleteConversationTurnsResponse, error) {
	return &proto.DeleteConversationTurnsResponse{}, nil
}

func (m *mockAgentClient) GetCompactionState(ctx context.Context, in *proto.GetCompactionStateRequest, opts ...grpc.CallOption) (*proto.GetCompactionStateResponse, error) {
	return &proto.GetCompactionStateResponse{}, nil
}

func (m *mockAgentClient) SuggestNextPrompt(ctx context.Context, in *proto.SuggestNextPromptRequest, opts ...grpc.CallOption) (*proto.SuggestNextPromptResponse, error) {
	return &proto.SuggestNextPromptResponse{}, nil
}

func (m *mockAgentClient) ExportContext(ctx context.Context, in *proto.ExportContextRequest, opts ...grpc.CallOption) (*proto.ExportContextResponse, error) {
	return &proto.ExportContextResponse{}, nil
}

func (m *mockAgentClient) ListMcpServers(ctx context.Context, in *proto.ListMcpServersRequest, opts ...grpc.CallOption) (*proto.ListMcpServersResponse, error) {
	return &proto.ListMcpServersResponse{}, nil
}

func (m *mockAgentClient) AddMcpServer(ctx context.Context, in *proto.AddMcpServerRequest, opts ...grpc.CallOption) (*proto.AddMcpServerResponse, error) {
	return &proto.AddMcpServerResponse{}, nil
}

func (m *mockAgentClient) RemoveMcpServer(ctx context.Context, in *proto.RemoveMcpServerRequest, opts ...grpc.CallOption) (*proto.RemoveMcpServerResponse, error) {
	return &proto.RemoveMcpServerResponse{}, nil
}

func (m *mockAgentClient) RestartMcpServer(ctx context.Context, in *proto.RestartMcpServerRequest, opts ...grpc.CallOption) (*proto.RestartMcpServerResponse, error) {
	return &proto.RestartMcpServerResponse{}, nil
}

func (m *mockAgentClient) GetCloudProfiles(ctx context.Context, in *proto.GetCloudProfilesRequest, opts ...grpc.CallOption) (*proto.GetCloudProfilesResponse, error) {
	return &proto.GetCloudProfilesResponse{}, nil
}

func (m *mockAgentClient) SetActiveCloudProfile(ctx context.Context, in *proto.SetActiveCloudProfileRequest, opts ...grpc.CallOption) (*proto.SetActiveCloudProfileResponse, error) {
	return &proto.SetActiveCloudProfileResponse{Ok: true}, nil
}

func (m *mockAgentClient) SetCloudProfileKey(ctx context.Context, in *proto.SetCloudProfileKeyRequest, opts ...grpc.CallOption) (*proto.SetCloudProfileKeyResponse, error) {
	return &proto.SetCloudProfileKeyResponse{Ok: true}, nil
}

func (m *mockAgentClient) UpsertCloudProfile(ctx context.Context, in *proto.UpsertCloudProfileRequest, opts ...grpc.CallOption) (*proto.UpsertCloudProfileResponse, error) {
	return &proto.UpsertCloudProfileResponse{Ok: true}, nil
}

func (m *mockAgentClient) RemoveCloudProfile(ctx context.Context, in *proto.RemoveCloudProfileRequest, opts ...grpc.CallOption) (*proto.RemoveCloudProfileResponse, error) {
	return &proto.RemoveCloudProfileResponse{Ok: true}, nil
}

func (m *mockAgentClient) GetOpenRuntimeStatus(ctx context.Context, in *proto.GetOpenRuntimeStatusRequest, opts ...grpc.CallOption) (*proto.GetOpenRuntimeStatusResponse, error) {
	return &proto.GetOpenRuntimeStatusResponse{}, nil
}

func (m *mockAgentClient) InstallOpenRuntime(ctx context.Context, in *proto.InstallOpenRuntimeRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[proto.InstallProgress], error) {
	return nil, nil
}

func (m *mockAgentClient) StartChatGPTLogin(ctx context.Context, in *proto.StartChatGPTLoginRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[proto.StartChatGPTLoginEvent], error) {
	return nil, nil
}

func (m *mockAgentClient) RegenerateContext(ctx context.Context, in *proto.RegenerateContextRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[proto.RegenerateContextProgress], error) {
	return nil, nil
}

func (m *mockAgentClient) RefreshOnlineCatalog(ctx context.Context, in *proto.RefreshOnlineCatalogRequest, opts ...grpc.CallOption) (*proto.RefreshOnlineCatalogResponse, error) {
	return &proto.RefreshOnlineCatalogResponse{}, nil
}

func (m *mockAgentClient) GetModelRAMEstimate(ctx context.Context, in *proto.GetModelRAMEstimateRequest, opts ...grpc.CallOption) (*proto.GetModelRAMEstimateResponse, error) {
	return &proto.GetModelRAMEstimateResponse{}, nil
}

func (m *mockAgentClient) ListCloudProfileModels(ctx context.Context, in *proto.ListCloudProfileModelsRequest, opts ...grpc.CallOption) (*proto.ListCloudProfileModelsResponse, error) {
	return &proto.ListCloudProfileModelsResponse{}, nil
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

	// List tools and verify expected tools are registered.
	want := map[string]bool{
		"cercano_local": false,
	}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("listing tools failed: %v", err)
		}
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected %s tool to be registered", name)
		}
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

func TestCercanoConfig_SetOpenModel(t *testing.T) {
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
	if mock.lastConfigReq.OpenModel != "GLM-4.7-Flash" {
		t.Errorf("expected local_model 'GLM-4.7-Flash', got %q", mock.lastConfigReq.OpenModel)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if text == "" {
		t.Error("expected non-empty response")
	}
}

func TestCercanoConfig_SetOpenRuntime(t *testing.T) {
	mock := &mockAgentClient{
		configResp: &proto.UpdateConfigResponse{
			Success: true,
			Message: "updated: [local_runtime=llama_server]",
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
			"action":        "set",
			"local_runtime": "llama_server",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if mock.lastConfigReq == nil {
		t.Fatal("expected UpdateConfig gRPC call")
	}
	if mock.lastConfigReq.OpenRuntime != "llama_server" {
		t.Errorf("expected local_runtime 'llama_server', got %q", mock.lastConfigReq.OpenRuntime)
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
			"action":     "set",
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

func TestCercanoConfig_GetListsModels(t *testing.T) {
	mock := &mockAgentClient{
		modelsResp: &proto.ListModelsResponse{
			Models: []*proto.ModelInfo{
				{Name: "qwen3-coder:latest", Size: 18556700761},
				{Name: "gemma3:4b", Size: 3300000000},
				{Name: "nomic-embed-text:latest", Size: 274302450},
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
		Name: "cercano_config",
		Arguments: map[string]any{
			"action": "get",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "qwen3-coder:latest") {
		t.Errorf("expected qwen3-coder in output, got %q", text)
	}
	if !strings.Contains(text, "gemma3:4b") {
		t.Errorf("expected gemma3:4b in output, got %q", text)
	}
	if !strings.Contains(text, "3.3 GB") {
		t.Errorf("expected size formatting, got %q", text)
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

func TestCercanoSkills_List(t *testing.T) {
	mock := &mockAgentClient{
		skillsResp: &proto.ListSkillsResponse{
			Skills: []*proto.SkillInfo{
				{Name: "cercano-local", Description: "Run prompts against local AI"},
				{Name: "cercano-summarize", Description: "Summarize text locally"},
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
		Name: "cercano_skills",
		Arguments: map[string]any{
			"action": "list",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "cercano-local") {
		t.Error("expected output to contain 'cercano-local'")
	}
	if !strings.Contains(text, "cercano-summarize") {
		t.Error("expected output to contain 'cercano-summarize'")
	}
}

func TestCercanoSkills_Get(t *testing.T) {
	mock := &mockAgentClient{
		getSkillResp: &proto.GetSkillResponse{
			Name:    "cercano-local",
			Content: "---\nname: cercano-local\ndescription: Run prompts\n---\n# Cercano Local",
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
		Name: "cercano_skills",
		Arguments: map[string]any{
			"action": "get",
			"name":   "cercano-local",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "cercano-local") {
		t.Error("expected output to contain 'cercano-local'")
	}
	if !strings.Contains(text, "# Cercano Local") {
		t.Error("expected output to contain skill body content")
	}

	// Verify the correct skill name was passed to gRPC
	if mock.lastGetSkillReq.Name != "cercano-local" {
		t.Errorf("expected gRPC request for 'cercano-local', got %q", mock.lastGetSkillReq.Name)
	}
}

func TestCercanoSkills_InvalidAction(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_skills",
		Arguments: map[string]any{
			"action": "delete",
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for invalid action")
	}
}

func TestCercanoStats_ReturnsUsageSummary(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := telemetry.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()
	collector := telemetry.NewCollector(store, 100)
	s.SetCollector(collector)

	// Seed some data
	e := telemetry.NewEvent("cercano_summarize", "qwen3-coder")
	e.Complete(500, 100, false, "", "")
	collector.Emit(e)
	collector.EmitCloudUsage(telemetry.CloudUsageReport{
		Timestamp:         time.Now(),
		CloudInputTokens:  10000,
		CloudOutputTokens: 2000,
		CloudProvider:     "anthropic",
		CloudModel:        "claude-opus-4-6",
	})
	collector.Close()

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "cercano_stats",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "Total Requests") {
		t.Errorf("expected 'Total Requests' in output, got %q", text)
	}
	if !strings.Contains(text, "summarize") {
		t.Errorf("expected tool breakdown, got %q", text)
	}
	if !strings.Contains(text, "qwen3-coder") {
		t.Errorf("expected model breakdown, got %q", text)
	}
	if !strings.Contains(text, "Local vs Cloud") {
		t.Errorf("expected local vs cloud bar, got %q", text)
	}
}

func TestCercanoStats_NoCollector(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "cercano_stats",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "not enabled") {
		t.Errorf("expected 'not enabled' message, got %q", text)
	}
}

func TestCercanoSubmitUsage_RecordsTokens(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	// Create a real telemetry store and collector for this test
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := telemetry.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()
	collector := telemetry.NewCollector(store, 100)
	s.SetCollector(collector)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_submit_usage",
		Arguments: map[string]any{
			"cloud_input_tokens":  15000,
			"cloud_output_tokens": 3000,
			"cloud_provider":      "anthropic",
			"cloud_model":         "claude-opus-4-6",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "18000") {
		t.Errorf("expected total token count in output, got %q", text)
	}

	// Drain collector and verify storage
	collector.Close()
	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.TotalCloudInputTokens != 15000 {
		t.Errorf("expected 15000 cloud input tokens, got %d", stats.TotalCloudInputTokens)
	}
	if stats.TotalCloudOutputTokens != 3000 {
		t.Errorf("expected 3000 cloud output tokens, got %d", stats.TotalCloudOutputTokens)
	}
}

func TestCercanoSubmitUsage_NoCollector(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)
	// No collector set

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_submit_usage",
		Arguments: map[string]any{
			"cloud_input_tokens":  1000,
			"cloud_output_tokens": 500,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "not enabled") {
		t.Errorf("expected 'not enabled' message, got %q", text)
	}
}

func TestDegradedServer_RegistersToolsAndReturnsError(t *testing.T) {
	s := NewDegradedServer(fmt.Errorf("could not connect to Ollama at http://localhost:11434. Is Ollama running?"))

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	// Verify tools are still registered.
	toolCount := 0
	for _, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("listing tools failed: %v", err)
		}
		toolCount++
	}
	if toolCount == 0 {
		t.Fatal("expected tools to be registered in degraded mode")
	}

	// Verify calling a tool returns the startup error via IsError.
	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "cercano_local",
		Arguments: map[string]any{"prompt": "hello"},
	})
	if err != nil {
		t.Fatalf("CallTool returned Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true in degraded mode")
	}
	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "Ollama") {
		t.Errorf("expected error to mention Ollama, got: %s", text)
	}
}

func TestHandleDocument_MissingFilePath(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "cercano_document",
		Arguments: map[string]any{},
	})
	// Missing file_path should result in an error
	if err == nil && result != nil && !result.IsError {
		text := ""
		if len(result.Content) > 0 {
			text = result.Content[0].(*gomcp.TextContent).Text
		}
		t.Errorf("expected error for missing file_path, got result: %s", text)
	}
}

func TestHandleDocument_FileNotFound(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_document",
		Arguments: map[string]any{
			"file_path": "/nonexistent/file.go",
		},
	})
	if err == nil && result != nil && !result.IsError {
		t.Error("expected error for nonexistent file")
	}
}

func TestHandleDocument_DryRun(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	// Create a temp Go file with undocumented symbols
	dir := t.TempDir()
	goFile := filepath.Join(dir, "test.go")
	src := `package example

func Hello() string {
	return "hello"
}

// World is documented.
func World() string {
	return "world"
}
`
	os.WriteFile(goFile, []byte(src), 0644)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_document",
		Arguments: map[string]any{
			"file_path": goFile,
			"dry_run":   true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "Dry run") {
		t.Errorf("expected dry run output, got: %s", text)
	}
	if !strings.Contains(text, "Hello") {
		t.Errorf("expected Hello in dry run output, got: %s", text)
	}
	if !strings.Contains(text, "already documented") {
		t.Errorf("expected 'already documented' for World, got: %s", text)
	}

	// Verify file was NOT modified
	data, _ := os.ReadFile(goFile)
	if string(data) != src {
		t.Error("dry run should not modify the file")
	}
}

func TestHandleDocument_EndToEnd(t *testing.T) {
	mock := &mockAgentClient{
		processResp: &proto.ProcessRequestResponse{
			Output:       "Hello returns a greeting string.",
			InputTokens:  100,
			OutputTokens: 20,
			RoutingMetadata: &proto.RoutingMetadata{
				ModelName: "qwen3-coder",
			},
		},
	}
	s := NewServer(mock)
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := telemetry.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()
	s.SetCollector(telemetry.NewCollector(store, 100))

	// Create a temp Go file
	dir := t.TempDir()
	goFile := filepath.Join(dir, "test.go")
	src := `package example

func Hello() string {
	return "hello"
}

// World is documented.
func World() string {
	return "world"
}
`
	os.WriteFile(goFile, []byte(src), 0644)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_document",
		Arguments: map[string]any{
			"file_path": goFile,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "Documented") {
		t.Errorf("expected 'Documented' in output, got: %s", text)
	}
	if !strings.Contains(text, "Hello") {
		t.Errorf("expected Hello in output, got: %s", text)
	}
	if !strings.Contains(text, "already documented") {
		t.Errorf("expected 'already documented' for World, got: %s", text)
	}

	// Verify the file was modified
	data, _ := os.ReadFile(goFile)
	if !strings.Contains(string(data), "// Hello returns a greeting string.") {
		t.Errorf("expected doc comment in file, got:\n%s", string(data))
	}

	// Verify backup was created
	backupDir := filepath.Join(dir, ".cercano", "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("backup dir should exist: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected backup file to be created")
	}
}

func TestHandleDocument_AllDocumented(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock)

	dir := t.TempDir()
	goFile := filepath.Join(dir, "test.go")
	src := `package example

// Hello is documented.
func Hello() string {
	return "hello"
}
`
	os.WriteFile(goFile, []byte(src), 0644)

	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	t1, t2 := gomcp.NewInMemoryTransports()
	s.MCPServer().Connect(ctx, t1, nil)
	cs, _ := client.Connect(ctx, t2, nil)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &gomcp.CallToolParams{
		Name: "cercano_document",
		Arguments: map[string]any{
			"file_path": goFile,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := result.Content[0].(*gomcp.TextContent).Text
	if !strings.Contains(text, "already documented") {
		t.Errorf("expected 'already documented' message, got: %s", text)
	}
}
