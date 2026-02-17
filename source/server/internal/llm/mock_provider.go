package llm

import (
	"context"

	"cercano/source/server/internal/agent"
)

type MockProvider struct {
	ProviderName string
}

func NewMockProvider(name string) *MockProvider {
	return &MockProvider{ProviderName: name}
}

func (m *MockProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	return &agent.Response{Output: "Processed by " + m.ProviderName + " (Mock)"}, nil
}

func (m *MockProvider) Name() string {
	return m.ProviderName
}
