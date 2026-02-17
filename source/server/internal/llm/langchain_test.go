package llm

import (
	"context"
	"testing"
	"cercano/source/server/internal/agent"
)

func TestCloudModelProvider_Interface(t *testing.T) {
	// This will fail to compile initially
	var _ agent.ModelProvider = (*CloudModelProvider)(nil)
}

func TestNewCloudModelProvider_Gemini(t *testing.T) {
	ctx := context.Background()
	// Test creating a Gemini provider (without real initialization for now)
	provider, err := NewCloudModelProvider(ctx, "google", "gemini-1.5-pro", "fake-key")
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if provider.Name() != "google" {
		t.Errorf("Expected name 'google', got '%s'", provider.Name())
	}
}

func TestNewCloudModelProvider_Anthropic(t *testing.T) {
	ctx := context.Background()
	provider, err := NewCloudModelProvider(ctx, "anthropic", "claude-3-opus", "fake-key")
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if provider.Name() != "anthropic" {
		t.Errorf("Expected name 'anthropic', got '%s'", provider.Name())
	}
}
