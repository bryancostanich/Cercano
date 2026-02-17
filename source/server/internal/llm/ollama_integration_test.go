package llm_test

import (
	"context"
	"os"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/router"
)

const integrationTestModelName = "qwen3-coder"

func TestOllamaProvider_Integration_Process(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test; set INTEGRATION_TEST=1 to run")
	}

	// Assume Ollama is running at localhost:11434
	provider := llm.NewOllamaProvider(integrationTestModelName, "http://localhost:11434")

	req := &router.Request{
		Input: "Write a simple Go function that adds two integers.",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := provider.Process(ctx, req)
	if err != nil {
		t.Fatalf("Integration test failed: %v", err)
	}

	if len(resp.Output) == 0 {
		t.Error("Expected non-empty output from model")
	}

	t.Logf("Model Output: %s", resp.Output)
}
