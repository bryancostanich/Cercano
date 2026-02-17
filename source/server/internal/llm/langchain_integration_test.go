package llm

import (
	"context"
	"os"
	"testing"
	"cercano/source/server/internal/agent"
)

func TestCloudModelProvider_Integration_Gemini(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test; set INTEGRATION_TEST=1 to run")
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Fatal("GEMINI_API_KEY environment variable not set")
	}

	ctx := context.Background()
	provider, err := NewCloudModelProvider(ctx, "google", "gemini-1.5-flash", apiKey)
	if err != nil {
		t.Fatalf("Failed to create Gemini provider: %v", err)
	}

	req := &agent.Request{Input: "Say 'Gemini integration test passed'"}
	res, err := provider.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if res.Output == "" {
		t.Error("Expected output, got empty string")
	}
	t.Logf("Gemini output: %s", res.Output)
}

func TestCloudModelProvider_Integration_Anthropic(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test; set INTEGRATION_TEST=1 to run")
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Fatal("ANTHROPIC_API_KEY environment variable not set")
	}

	ctx := context.Background()
	provider, err := NewCloudModelProvider(ctx, "anthropic", "claude-3-haiku-20240307", apiKey)
	if err != nil {
		t.Fatalf("Failed to create Anthropic provider: %v", err)
	}

	req := &agent.Request{Input: "Say 'Anthropic integration test passed'"}
	res, err := provider.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if res.Output == "" {
		t.Error("Expected output, got empty string")
	}
	t.Logf("Anthropic output: %s", res.Output)
}
