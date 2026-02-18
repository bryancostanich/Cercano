package agent

import (
	"context"
	"testing"
)

type mockRouter struct {
	intent Intent
	provider ModelProvider
}

func (m *mockRouter) SelectProvider(req *Request) (ModelProvider, error) {
	return m.provider, nil
}

func (m *mockRouter) ClassifyIntent(req *Request) (Intent, error) {
	return m.intent, nil
}

type mockModelProvider struct{ name string }
func (m *mockModelProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return &Response{Output: "provider output"}, nil
}
func (m *mockModelProvider) Name() string { return m.name }

type mockCoordinator struct{}
func (m *mockCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string) (*Response, error) {
	return &Response{Output: "coordinated output"}, nil
}

func TestAgent_ProcessRequest_ChatIntent(t *testing.T) {
	router := &mockRouter{intent: IntentChat, provider: &mockModelProvider{name: "mock"}}
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
		Input: "write code",
		WorkDir: "/tmp",
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
