package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

// MockRoundTripper is a mock http.RoundTripper for testing.
type mockRoundTripper struct {
	responses map[string]string // Maps prompt to JSON response
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewBuffer(body))

	// Simplified: just match the body string to find the response
	for prompt, resp := range m.responses {
		if bytes.Contains(body, []byte(prompt)) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(resp)),
				Header:     make(http.Header),
			}, nil
		}
	}

	// Default response if not found
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(`{"embedding": [0.0, 0.0]}`)),
		Header:     make(http.Header),
	}, nil
}

func TestRouter_ClassifiesUnitTestGenerationAsLocal(t *testing.T) {
	// Setup Mocks
	localProvider := &MockModelProvider{name: "LocalModel"}
	cloudProvider := &MockModelProvider{name: "CloudModel"}

	mockResponses := map[string]string{
		// Intent Prototypes
		"Rename the variable": `{"embedding": [1.0, 0.0]}`, // Intent:Coding
		"Explain what":        `{"embedding": [0.0, 1.0]}`, // Intent:Chat

		// Provider Prototypes
		"Format this file":   `{"embedding": [1.0, 0.0]}`, // Provider:Local
		"How do I implement": `{"embedding": [0.0, 1.0]}`, // Provider:Cloud

		// High Complexity Tasks
		"Refactor this massive monolithic": `{"embedding": [0.5, 0.5]}`,
		"Implement a complex raft":        `{"embedding": [0.5, 0.5]}`,
	}

	mockClient := &http.Client{
		Transport: &mockRoundTripper{responses: mockResponses},
	}

	// Initialize Router with mocked client
	r, err := NewSmartRouter(localProvider, cloudProvider, "nomic-embed-text", mockClient, "prototypes.yaml", func(ctx context.Context, provider, model, apiKey string) (ModelProvider, error) {
		return &MockModelProvider{name: provider}, nil
	})
	if err != nil {
		t.Fatalf("Failed to create router: %v", err)
	}

	testCases := []struct {
		input          string
		expectedSource string
		expectedIntent Intent
		mockEmbedding  string
	}{
		{
			input:          "Generate unit tests for this function",
			expectedSource: "LocalModel",
			expectedIntent: IntentCoding,
			mockEmbedding:  `{"embedding": [1.0, 0.0]}`, // Matches Coding Intent and Local Provider
		},
		{
			input:          "Explain how black holes work",
			expectedSource: "CloudModel",
			expectedIntent: IntentChat,
			mockEmbedding:  `{"embedding": [0.0, 1.0]}`, // Matches Chat Intent and Cloud Provider
		},
		{
			input:          "Refactor this massive monolithic service into a distributed system with high availability",
			expectedSource: "CloudModel",
			expectedIntent: IntentCoding,
			mockEmbedding:  `{"embedding": [0.5, 0.5]}`, // Matches both per prototypes.yaml
		},
	}

	for _, tc := range testCases {
		mockResponses[tc.input] = tc.mockEmbedding

		req := &Request{Input: tc.input}
		provider, err := r.SelectProvider(req)
		if err != nil {
			t.Errorf("Router failed for input '%s': %v", tc.input, err)
			continue
		}

		if provider.Name() != tc.expectedSource {
			t.Errorf("Input: '%s'\nExpected Provider: %s\nGot Provider: %s", tc.input, tc.expectedSource, provider.Name())
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
