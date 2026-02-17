package llm

import (
	"context"

	"cercano/source/server/internal/router"
)

type MockProvider struct {
	ProviderName string
}

func NewMockProvider(name string) *MockProvider {
	return &MockProvider{ProviderName: name}
}

func (m *MockProvider) Process(ctx context.Context, req *router.Request) (*router.Response, error) {
	return &router.Response{Output: "Processed by " + m.ProviderName + " (Mock)"}, nil
}

func (m *MockProvider) Name() string {
	return m.ProviderName
}
