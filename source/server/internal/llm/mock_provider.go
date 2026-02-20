package llm

import (
	"context"
	"strings"

	"cercano/source/server/internal/agent"
)

type MockProvider struct {
	ProviderName string
}

func NewMockProvider(name string) *MockProvider {
	return &MockProvider{ProviderName: name}
}

func (m *MockProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	output := "Processed by " + m.ProviderName + " (Mock)"
	
	// If it looks like a Go request, return a minimal valid Go file
	if strings.Contains(strings.ToLower(req.Input), "package") || strings.Contains(strings.ToLower(req.Input), "func") {
		output = "package mock\n\n// Mock code from " + m.ProviderName + "\nfunc Mock() {}"
	}
	
	return &agent.Response{Output: output}, nil
}

func (m *MockProvider) Name() string {
	return m.ProviderName
}
