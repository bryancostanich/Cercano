package agent

import (
	"context"
	"testing"
)

type mockRouter struct {
	intent         Intent
	provider       ModelProvider
	ModelProviders map[string]ModelProvider
}

func (m *mockRouter) SelectProvider(req *Request, intent Intent) (ModelProvider, error) {
	return m.provider, nil
}

func (m *mockRouter) ClassifyIntent(req *Request) (Intent, error) {
	return m.intent, nil
}

func (m *mockRouter) GetModelProviders() map[string]ModelProvider {
	return m.ModelProviders
}

type mockModelProvider struct{ name string }

func (m *mockModelProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return &Response{Output: "provider output"}, nil
}
func (m *mockModelProvider) Name() string { return m.name }

type mockCoordinator struct{}

func (m *mockCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error) {
	return &Response{Output: "coordinated output"}, nil
}

func TestAgent_ProcessRequest_ChatIntent(t *testing.T) {
	router := &mockRouter{intent: IntentChat, provider: &mockModelProvider{name: "mock"}}
	// Mock coordinator needs the new signature if initialized via NewGenerationCoordinator, 
	// but here it's a mock struct implementing the interface.
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	ctx := context.Background()
	res, err := a.ProcessRequest(ctx, &Request{Input: "hello"})

	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if res.Output != "provider output" {
		t.Errorf("Expected provider output, got %s", res.Output)
	}
}

func TestAgent_ProcessRequest_CodingIntent(t *testing.T) {
	router := &mockRouter{intent: IntentCoding, provider: &mockModelProvider{name: "mock"}}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	ctx := context.Background()
	req := &Request{
		Input:    "write code",
		WorkDir:  "/tmp",
		FileName: "test.go",
	}
	res, err := a.ProcessRequest(ctx, req)

	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if res.Output != "coordinated output" {
		t.Errorf("Expected coordinated output, got %s", res.Output)
	}
}

func TestAgent_ProcessRequest_UnitTestFilenameAdjustment(t *testing.T) {
	router := &mockRouter{intent: IntentCoding, provider: &mockModelProvider{name: "mock"}}
	
	capturedFile := ""
	coordinator := &MockCoordinator{
		CoordinateFunc: func(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error) {
			capturedFile = fileName
			return &Response{Output: "ok"}, nil
		},
	}
	a := NewAgent(router, coordinator)

	ctx := context.Background()
	req := &Request{
		Input:    "Generate unit tests for this",
		WorkDir:  "/tmp",
		FileName: "logic.go",
	}
	_, err := a.ProcessRequest(ctx, req)

	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if capturedFile != "logic_test.go" {
		t.Errorf("Expected logic_test.go for singular, got %s", capturedFile)
	}

	// Test plural
	req.Input = "Generate unit tests"
	_, err = a.ProcessRequest(ctx, req)
	if capturedFile != "logic_test.go" {
		t.Errorf("Expected logic_test.go for plural, got %s", capturedFile)
	}
}

type MockCoordinator struct {
	CoordinateFunc func(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error)
}

func (m *MockCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error) {
	return m.CoordinateFunc(ctx, instruction, inputCode, workDir, fileName, progress)
}

func TestAgent_ProcessRequest_ExplicitCloudOverride(t *testing.T) {
	// Router says LocalModel, but input says "use cloud"
	cloudProvider := &mockModelProvider{name: "CloudModel"}
	router := &mockRouter{
		intent:   IntentChat,
		provider: &mockModelProvider{name: "LocalModel"},
		ModelProviders: map[string]ModelProvider{
			"CloudModel": cloudProvider,
		},
	}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	ctx := context.Background()
	// Input contains "use cloud"
	res, err := a.ProcessRequest(ctx, &Request{Input: "use cloud to explain black holes"})

	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	// We expect it to be processed by CloudModel even though router said Local
	if res.RoutingMetadata.ModelName != "CloudModel" {
		t.Errorf("Expected CloudModel override, got %s", res.RoutingMetadata.ModelName)
	}
}
