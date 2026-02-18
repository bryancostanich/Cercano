package agent_test

import (
	"context"
	"net/http"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/llm"
)

func TestRouter_ClassifiesUnitTestGenerationAsLocal(t *testing.T) {
	// Setup Mocks
	localProvider := llm.NewMockProvider("LocalModel")
	cloudProvider := llm.NewMockProvider("CloudModel")

	// Initialize Router with real prototypes and embedding model (requires Ollama)
	r, err := agent.NewSmartRouter(localProvider, cloudProvider, "nomic-embed-text", http.DefaultClient, "prototypes.yaml", func(ctx context.Context, provider, model, apiKey string) (agent.ModelProvider, error) {
		return llm.NewMockProvider(provider), nil
	})
	if err != nil {
		t.Fatalf("Failed to create router: %v", err)
	}

	testCases := []struct {
		input          string
		expectedSource string
		expectedIntent agent.Intent
	}{
		{"Generate unit tests for this function", "LocalModel", agent.IntentCoding},
		{"Write a table driven test for router.go", "LocalModel", agent.IntentCoding},
		{"Create a test file for the server package", "LocalModel", agent.IntentCoding},
		{"Explain how black holes work", "CloudModel", agent.IntentChat}, // Control case
	}

	for _, tc := range testCases {
		req := &agent.Request{Input: tc.input}
		provider, err := r.SelectProvider(req)
		if err != nil {
			t.Errorf("Router failed for input '%s': %v", tc.input, err)
			continue
		}

		if provider.Name() != tc.expectedSource {
			t.Errorf("Input: '%s'\nExpected: %s\nGot: %s", tc.input, tc.expectedSource, provider.Name())
		}

		intent, err := r.ClassifyIntent(req)
		if err != nil {
			t.Errorf("ClassifyIntent failed for input '%s': %v", tc.input, err)
			continue
		}
		if intent != tc.expectedIntent {
			t.Errorf("Input: '%s'\nExpected Intent: %s\nGot Intent: %s", tc.input, tc.expectedIntent, intent)
		}
	}
}