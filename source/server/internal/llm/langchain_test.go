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

func TestResolveModel(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		expected string
	}{
		{"empty model defaults google", "google", "", "gemini-3-flash"},
		{"empty model defaults anthropic", "anthropic", "", "claude-sonnet-4-6"},
		{"valid google model kept", "google", "gemini-1.5-pro", "gemini-1.5-pro"},
		{"valid anthropic model kept", "anthropic", "claude-3-opus", "claude-3-opus"},
		{"gemini model on anthropic corrected", "anthropic", "gemini-3-flash-preview", "claude-sonnet-4-6"},
		{"claude model on google corrected", "google", "claude-3-opus", "gemini-3-flash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveModel(tt.provider, tt.model)
			if got != tt.expected {
				t.Errorf("resolveModel(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.expected)
			}
		})
	}
}
