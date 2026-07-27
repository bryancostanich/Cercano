package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/engine/ollama"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/protocols"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

var lis *bufconn.Listener

// Mocks for testing
type mockProvider struct {
	name string
}

func (m *mockProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	return &agent.Response{Output: "Processed by " + m.name}, nil
}

func (m *mockProvider) Name() string {
	return m.name
}

type mockRouter struct{}

func (m *mockRouter) SelectProvider(req *agent.Request, intent agent.Intent) (agent.TurnRunner, error) {
	return &mockProvider{name: "MockLocal"}, nil
}

func (m *mockRouter) ClassifyIntent(req *agent.Request) (agent.Intent, error) {
	return agent.IntentChat, nil
}

func (m *mockRouter) Tiers() agent.Tiers {
	return agent.Tiers{
		Open:  &mockProvider{name: "MockLocal"},
		Cloud: &mockProvider{name: "MockCloud"},
	}
}

type mockCoordinator struct{}

func (m *mockCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress agent.ProgressFunc) (*agent.Response, error) {
	return &agent.Response{Output: "coordinated output"}, nil
}

func init() {
	lis = bufconn.Listen(bufSize)
	s := grpc.NewServer()
	coordinator := &mockCoordinator{}
	orchestrator := agent.NewAgent(&mockRouter{}, coordinator)
	proto.RegisterAgentServer(s, NewServer(orchestrator, nil, nil, nil, nil))
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("Server exited with error: %v", err)
		}
	}()
}

func bufDialer(context.Context, string) (net.Conn, error) {
	return lis.Dial()
}

func TestAgentServer_ProcessRequest(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(bufDialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	client := proto.NewAgentClient(conn)

	// Test case 1: Basic request
	req := &proto.ProcessRequestRequest{Input: "Hello AI"}
	res, err := client.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if res.Output == "" {
		t.Errorf("Expected output, got empty string")
	}
}

func TestListModels(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []map[string]interface{}{
				{"name": "qwen3-coder:latest", "size": 4700000000, "modified_at": "2026-03-15T10:30:00Z"},
				{"name": "llama3:latest", "size": 8100000000, "modified_at": "2026-03-14T09:00:00Z"},
			},
		})
	})
	mockOllama := httptest.NewServer(handler)
	defer mockOllama.Close()

	registry := engine.NewEngineRegistry()
	eng := ollama.NewOllamaEngine(mockOllama.URL)
	registry.RegisterEngine(eng)

	srv := NewServer(nil, nil, nil, nil, registry)

	resp, err := srv.ListModels(context.Background(), &proto.ListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("Expected 2 models, got %d", len(resp.Models))
	}
}

func TestGetRuntimeStatus_IncludesConfiguredEndpoints(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.SetRuntimeManager(localruntime.NewManager())
	srv.SetConfigPersistence("", config.Config{
		OllamaURL:      "http://mac-studio.local:11434",
		OpenModel:      "qwen3-coder",
		EmbeddingModel: "nomic-embed-text",
		CloudProvider:  "anthropic",
		CloudModel:     "claude-test",
		CloudBaseURL:   "http://127.0.0.1:3456",
	})

	resp, err := srv.GetRuntimeStatus(context.Background(), &proto.GetRuntimeStatusRequest{})
	if err != nil {
		t.Fatalf("GetRuntimeStatus failed: %v", err)
	}
	if len(resp.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %#v", resp.Endpoints)
	}
	if resp.Endpoints[0].GetId() != "ollama" || resp.Endpoints[0].GetScope() != "lan" {
		t.Fatalf("unexpected ollama endpoint: %#v", resp.Endpoints[0])
	}
	if resp.Endpoints[1].GetKind() != "anthropic_proxy" {
		t.Fatalf("unexpected cloud endpoint: %#v", resp.Endpoints[1])
	}
}

func TestMapResponse_IncludesEndpoint(t *testing.T) {
	registry := engine.NewEngineRegistry()
	eng := ollama.NewOllamaEngine("http://localhost:11434")
	registry.RegisterEngine(eng)

	eng.SetBaseURL("http://remote:11434")
	srv := NewServer(nil, nil, nil, nil, registry)

	agentResp := &agent.Response{
		Output: "test output",
		RoutingMetadata: agent.RoutingMetadata{
			ModelName:  "test-model",
			Confidence: 1.0,
		},
	}

	protoResp := srv.MapResponseForTest(agentResp)

	if protoResp.RoutingMetadata.Endpoint != "http://remote:11434" {
		t.Errorf("Expected endpoint 'http://remote:11434', got %q", protoResp.RoutingMetadata.Endpoint)
	}
	if protoResp.RoutingMetadata.IsFallback {
		t.Error("Expected IsFallback=false when using primary")
	}

	// Switch to fallback and verify
	eng.SwitchToFallback()
	protoResp = srv.MapResponseForTest(agentResp)

	if protoResp.RoutingMetadata.Endpoint != "http://localhost:11434" {
		t.Errorf("Expected fallback endpoint 'http://localhost:11434', got %q", protoResp.RoutingMetadata.Endpoint)
	}
	if !protoResp.RoutingMetadata.IsFallback {
		t.Error("Expected IsFallback=true when using fallback")
	}
}

func TestUpdateConfig_OllamaURL(t *testing.T) {
	registry := engine.NewEngineRegistry()
	eng := ollama.NewOllamaEngine("http://localhost:11434")
	registry.RegisterEngine(eng)
	srv := NewServer(nil, nil, nil, nil, registry)

	// Set a valid remote URL
	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		OllamaUrl: "http://mac-studio.local:11434",
	})
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if !resp.Success {
		t.Errorf("Expected success, got: %s", resp.Message)
	}

	// Verify the provider's BaseURL was updated
	if eng.GetActiveURL() != "http://mac-studio.local:11434" {
		t.Errorf("Expected BaseURL 'http://mac-studio.local:11434', got '%s'", eng.GetActiveURL())
	}
}

func TestUpdateConfig_OllamaURL_InvalidURL(t *testing.T) {
	registry := engine.NewEngineRegistry()
	eng := ollama.NewOllamaEngine("http://localhost:11434")
	registry.RegisterEngine(eng)

	srv := NewServer(nil, nil, nil, nil, registry)

	// Set an invalid URL — should fail validation
	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		OllamaUrl: "not-a-valid-url",
	})
	if err != nil {
		t.Fatalf("UpdateConfig returned error: %v", err)
	}
	if resp.Success {
		t.Error("Expected failure for invalid URL, got success")
	}

	if eng.GetActiveURL() != "http://localhost:11434" {
		t.Errorf("Expected BaseURL unchanged, got '%s'", eng.GetActiveURL())
	}
}

func TestListSkills(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)

	resp, err := srv.ListSkills(context.Background(), &proto.ListSkillsRequest{})
	if err != nil {
		t.Fatalf("ListSkills failed: %v", err)
	}
	if len(resp.Skills) == 0 {
		t.Fatal("Expected at least one skill, got none")
	}

	// Verify tool skills and every built-in protocol skill are present.
	expectedSkills := map[string]bool{
		"cercano-local":     false,
		"cercano-models":    false,
		"cercano-config":    false,
		"cercano-summarize": false,
		"cercano-extract":   false,
		"cercano-classify":  false,
		"cercano-explain":   false,
	}
	for _, p := range protocols.Builtins() {
		expectedSkills[p.Name] = false
	}

	for _, skill := range resp.Skills {
		if _, ok := expectedSkills[skill.Name]; ok {
			expectedSkills[skill.Name] = true
		}
		if skill.Description == "" {
			t.Errorf("Skill %q has empty description", skill.Name)
		}
	}

	for name, found := range expectedSkills {
		if !found {
			t.Errorf("Expected skill %q not found in catalog", name)
		}
	}
}

func TestGetSkill(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)

	resp, err := srv.GetSkill(context.Background(), &proto.GetSkillRequest{Name: "cercano-local"})
	if err != nil {
		t.Fatalf("GetSkill failed: %v", err)
	}
	if resp.Name != "cercano-local" {
		t.Errorf("Expected name 'cercano-local', got %q", resp.Name)
	}
	if resp.Content == "" {
		t.Error("Expected non-empty content")
	}
	// Content should contain the SKILL.md frontmatter
	if !containsString(resp.Content, "name: cercano-local") {
		t.Error("Content should contain frontmatter with name field")
	}
}

func TestGetSkill_Protocol(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)

	resp, err := srv.GetSkill(context.Background(), &proto.GetSkillRequest{Name: "systematic-debugging"})
	if err != nil {
		t.Fatalf("GetSkill protocol failed: %v", err)
	}
	if resp.Name != "systematic-debugging" {
		t.Errorf("Expected name 'systematic-debugging', got %q", resp.Name)
	}
	if !containsString(resp.Content, "name: systematic-debugging") {
		t.Error("protocol skill should contain frontmatter with name field")
	}
	if !containsString(resp.Content, "# Systematic Debugging Protocol") {
		t.Error("protocol skill should contain the protocol body")
	}
}

func TestGetSkill_NotFound(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)

	_, err := srv.GetSkill(context.Background(), &proto.GetSkillRequest{Name: "nonexistent-skill"})
	if err == nil {
		t.Fatal("Expected error for nonexistent skill, got nil")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestUpdateConfig_OllamaURL_WithModel(t *testing.T) {
	registry := engine.NewEngineRegistry()
	eng := ollama.NewOllamaEngine("http://localhost:11434")
	registry.RegisterEngine(eng)
	srv := NewServer(nil, nil, nil, nil, registry)

	// Set both URL and model in one call
	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		OllamaUrl: "http://192.168.1.100:11434",
		OpenModel: "llama3",
	})
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if !resp.Success {
		t.Errorf("Expected success, got: %s", resp.Message)
	}

	if eng.GetActiveURL() != "http://192.168.1.100:11434" {
		t.Errorf("Expected BaseURL 'http://192.168.1.100:11434', got '%s'", eng.GetActiveURL())
	}
}

func TestUpdateConfig_OpenRuntime(t *testing.T) {
	registry := engine.NewEngineRegistry()
	ollamaEng := &namedTestEngine{name: "ollama"}
	llamaEng := &namedTestEngine{name: "llama_server"}
	registry.RegisterEngine(ollamaEng)
	registry.RegisterEngine(llamaEng)
	srv := NewServer(nil, nil, nil, nil, registry)
	srv.SetConfigPersistence("", config.Config{
		OpenRuntime: "ollama",
		OpenModel:   "ollama-model",
		LlamaServer: config.LlamaServerConfig{
			DefaultModel: "/models/model-a.gguf",
		},
	})

	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		OpenRuntime: "llama_server",
	})
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got: %s", resp.Message)
	}
	cfg, err := srv.GetConfig(context.Background(), &proto.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfg.GetOpenRuntime() != "llama_server" {
		t.Fatalf("local runtime not persisted in server state: %q", cfg.GetOpenRuntime())
	}
}

func TestUpdateConfig_WatchdogEnable(t *testing.T) {
	eng := dispatch.NewEngine(
		func() dispatch.Providers { return dispatch.Providers{} },
		func() locus.Mode { return locus.OpenOnly },
		nil,
	)
	srv := NewServer(nil, nil, nil, nil, engine.NewEngineRegistry())
	srv.SetDispatchEngine(eng)
	srv.SetConfigPersistence("", config.Config{})

	// enabling builds a live watchdog
	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogEnabled: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got: %s", resp.Message)
	}
	if !srv.cfgSvc.Get().Watchdog.Enabled {
		t.Fatal("Enabled not applied")
	}
	if srv.watchdog == nil {
		t.Fatal("watchdog not rebuilt/active after enable")
	}

	// disabling tears it back down
	if _, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogEnabled: "false"}); err != nil {
		t.Fatal(err)
	}
	if srv.cfgSvc.Get().Watchdog.Enabled {
		t.Fatal("Enabled not cleared")
	}
	if srv.watchdog != nil {
		t.Fatal("watchdog should be nil after disable")
	}
}

func TestUpdateConfig_AgentShutdownOnLastClient(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, engine.NewEngineRegistry())
	srv.SetConfigPersistence("", config.Config{})

	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{AgentShutdownOnLastClient: "false"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got: %s", resp.Message)
	}
	if srv.cfgSvc.Get().Agent.ShutdownOnLastClient {
		t.Fatal("agent.shutdown_on_last_client not cleared")
	}
	cfg, err := srv.GetConfig(context.Background(), &proto.GetConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GetAgentShutdownOnLastClient() {
		t.Fatal("GetConfig should report agent_shutdown_on_last_client=false")
	}

	resp, err = srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{AgentShutdownOnLastClient: "maybe"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Fatal("expected invalid agent_shutdown_on_last_client to fail")
	}
}

func TestUpdateConfig_ToolLoopMaxIterations(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, engine.NewEngineRegistry())
	srv.SetConfigPersistence("", config.Config{})

	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{ToolLoopMaxIterations: "-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got: %s", resp.Message)
	}
	if got := srv.cfgSvc.Get().ToolLoop.MaxIterations; got != config.UnlimitedToolLoopMaxIterations {
		t.Fatalf("stored max iterations = %d, want unlimited sentinel", got)
	}
	cfg, err := srv.GetConfig(context.Background(), &proto.GetConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.GetToolLoopMaxIterations(); got != int32(config.UnlimitedToolLoopMaxIterations) {
		t.Fatalf("GetConfig max iterations = %d, want unlimited sentinel", got)
	}

	resp, err = srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{ToolLoopMaxIterations: "-2"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Fatalf("expected invalid value to fail")
	}
}

func TestUpdateConfig_WatchdogEcho_and_GetConfig(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, engine.NewEngineRegistry())
	srv.SetConfigPersistence("", config.Config{})

	if _, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogEcho: "true"}); err != nil {
		t.Fatal(err)
	}
	if !srv.cfgSvc.Get().Watchdog.Echo {
		t.Fatal("Echo not applied")
	}

	resp, err := srv.GetConfig(context.Background(), &proto.GetConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetWatchdogEcho() {
		t.Fatal("GetConfig did not report echo")
	}
}

func TestUpdateConfig_OpenRuntime_Invalid(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, engine.NewEngineRegistry())
	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		OpenRuntime: "tensor_vibes",
	})
	if err != nil {
		t.Fatalf("UpdateConfig returned error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected invalid local runtime to fail")
	}
}

type namedTestEngine struct {
	name string
}

func (e *namedTestEngine) Name() string { return e.name }

func (e *namedTestEngine) Complete(ctx context.Context, model, prompt, systemPrompt string, opts engine.GenOptions) (engine.CompletionResult, error) {
	return engine.CompletionResult{Output: e.name + ":" + model + ":" + prompt}, nil
}

func (e *namedTestEngine) CompleteStream(ctx context.Context, model, prompt, systemPrompt string, opts engine.GenOptions, onToken func(string)) (engine.CompletionResult, error) {
	return e.Complete(ctx, model, prompt, systemPrompt, opts)
}

func (e *namedTestEngine) ChatWithTools(ctx context.Context, req engine.ChatRequest) (engine.ChatResponse, error) {
	return engine.ChatResponse{Content: e.name}, nil
}

func (e *namedTestEngine) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	return []engine.ModelInfo{{Name: e.name + "-model"}}, nil
}

func TestUpdateConfig_WatchdogModeChecksEscalate(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, engine.NewEngineRegistry())
	srv.SetConfigPersistence("", config.Config{})

	_, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		WatchdogMode: "strict", WatchdogEscalateAfter: "3", WatchdogChecks: "systematic-debugging,plain-english",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := srv.cfgSvc.Get().Watchdog
	if w.Mode != "strict" {
		t.Fatalf("mode=%q", w.Mode)
	}
	if w.EscalateAfter != 3 {
		t.Fatalf("escalate=%d", w.EscalateAfter)
	}
	if strings.Join(w.Checks, ",") != "systematic-debugging,plain-english" {
		t.Fatalf("checks=%v", w.Checks)
	}

	// "-" sentinel → empty list
	if _, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogChecks: "-"}); err != nil {
		t.Fatal(err)
	}
	if len(srv.cfgSvc.Get().Watchdog.Checks) != 0 {
		t.Fatalf("expected empty checks, got %v", srv.cfgSvc.Get().Watchdog.Checks)
	}

	// invalid escalate ignored
	before := srv.cfgSvc.Get().Watchdog.EscalateAfter
	if _, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogEscalateAfter: "nope"}); err != nil {
		t.Fatal(err)
	}
	if srv.cfgSvc.Get().Watchdog.EscalateAfter != before {
		t.Fatal("invalid escalate must be ignored")
	}

	// invalid mode ignored
	if _, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogMode: "permissive"}); err != nil {
		t.Fatal(err)
	}
	if srv.cfgSvc.Get().Watchdog.Mode != "strict" {
		t.Fatalf("invalid mode must be ignored, got %q", srv.cfgSvc.Get().Watchdog.Mode)
	}

	// GetConfig reports them
	resp, err := srv.GetConfig(context.Background(), &proto.GetConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetWatchdogMode() != "strict" || resp.GetWatchdogEscalateAfter() != "3" {
		t.Fatalf("GetConfig: mode=%q escalate=%q", resp.GetWatchdogMode(), resp.GetWatchdogEscalateAfter())
	}
}
