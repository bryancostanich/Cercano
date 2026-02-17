package agent_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
)

const integrationTestModelName = "nomic-embed-text"

// TestSmartRouter_Integration_SelectProvider performs an integration test
// against a live Ollama instance with a local model.
func TestSmartRouter_Integration_SelectProvider(t *testing.T) {
	// Skip this test if the 'integration' build tag is not present
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test; set INTEGRATION_TEST=1 to run")
	}

	time.Sleep(1 * time.Second)

	mockLocal := &mockModelProvider{name: "LocalModel"}
	mockCloud := &mockModelProvider{name: "CloudModel"}

	smartRouter, err := agent.NewSmartRouter(mockLocal, mockCloud, integrationTestModelName, nil, "prototypes.yaml", func(ctx context.Context, provider, model, apiKey string) (agent.ModelProvider, error) {
		return &mockModelProvider{name: provider}, nil
	})
	if err != nil {
		t.Fatalf("Failed to create SmartRouter: %v", err)
	}

	testCases := []struct {
		name                 string
		input                string
		expectedClassification string
	}{
		// --- LocalModel (Novel Phrasings) ---
		{
			name:                 "Refactor: Extract method",
			input:                "Pull the logic inside this for-loop out into a separate function called 'processItem'.",
			expectedClassification: "LocalModel",
		},
		{
			name:                 "File System: Cleanup",
			input:                "Get rid of all the .tmp files in the current folder.",
			expectedClassification: "LocalModel",
		},
		{
			name:                 "Analysis: Complexity",
			input:                "Calculate the cyclomatic complexity of the 'NewSmartRouter' function.",
			expectedClassification: "LocalModel",
		},
		{
			name:                 "Editing: Typo fix",
			input:                "Fix the spelling mistake in the variable 'threshold'.",
			expectedClassification: "LocalModel",
		},

		// --- CloudModel (Novel Phrasings) ---
		{
			name:                 "Knowledge: Algorithms",
			input:                "Explain how a bloom filter works and when I should use one.",
			expectedClassification: "CloudModel",
		},
		{
			name:                 "System Design: Scalability",
			input:                "What strategies can I use to shard a Postgres database without downtime?",
			expectedClassification: "CloudModel",
		},
		{
			name:                 "Creative: Marketing",
			input:                "Draft a tweet announcing the launch of our new AI tool.",
			expectedClassification: "CloudModel",
		},
		
		// --- Fallback / Ambiguous (Should default to CloudModel) ---
		{
			name:                 "Ambiguous: Hello",
			input:                "Hello there.",
			expectedClassification: "CloudModel",
		},
		{
			name:                 "Ambiguous: Gibberish",
			input:                "xyz 123 foo bar baz qux.",
			expectedClassification: "CloudModel",
		},
		{
			name:                 "Ambiguous: Philosophical",
			input:                "What is the meaning of life?",
			expectedClassification: "CloudModel",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("--- Testing: %s ---", tc.name)
			t.Logf("Input: %s", tc.input)

			req := &agent.Request{Input: tc.input}
			selectedProvider, err := smartRouter.SelectProvider(req)
			if err != nil {
				t.Errorf("SelectProvider returned an error: %v", err)
				return
			}

			t.Logf("SmartRouter classified as: %s", selectedProvider.Name())
			if selectedProvider.Name() != tc.expectedClassification {
				t.Errorf("Incorrect classification. Expected '%s', got '%s'", tc.expectedClassification, selectedProvider.Name())
			}
		})
	}}

// mockModelProvider is a mock implementation of the agent.ModelProvider interface for testing.
type mockModelProvider struct {
	name string
	err  error
}

func (m *mockModelProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &agent.Response{Output: fmt.Sprintf("Processed by %s: %s", m.name, req.Input)}, nil
}

func (m *mockModelProvider) Name() string {
	return m.name
}