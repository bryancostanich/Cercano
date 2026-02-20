package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
)

// MockModelProvider is a mock implementation of the ModelProvider interface for testing.
type MockModelProvider struct {
	name string
	err  error
}

func (m *MockModelProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &Response{Output: fmt.Sprintf("Processed by %s: %s", m.name, req.Input)}, nil
}

func (m *MockModelProvider) Name() string {
	return m.name
}

// MockRoundTripper is a mock http.RoundTripper for testing.
type MockRoundTripper struct {
	responses map[string]string // Maps prompt to JSON response
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
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

	return nil, fmt.Errorf("no mock response for body: %s", string(body))
}

func TestSmartRouter_ClassifyIntent(t *testing.T) {
	// Setup same router as above
	protoContent := `
intents:
  coding:
    - "generate code"
  chat:
    - "explain this"
providers:
  local:
    - "local task"
  cloud:
    - "cloud task"
`
	tmpFile, err := os.CreateTemp("", "prototypes*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write([]byte(protoContent))
	tmpFile.Close()

	mockResponses := map[string]string{
		"generate code": `{"embedding": [1.0, 0.0]}`,
		"explain this":  `{"embedding": [0.0, 1.0]}`,
		"local task":    `{"embedding": [1.0, 0.0]}`,
		"cloud task":    `{"embedding": [0.0, 1.0]}`,
	}
	mockClient := &http.Client{
		Transport: &MockRoundTripper{responses: mockResponses},
	}

	router, _ := NewSmartRouter(nil, nil, "nomic-embed-text", mockClient, tmpFile.Name(), nil)

	tests := []struct {
		input          string
		expectedIntent Intent
		mockResponse   string
	}{
		{
			input:          "write me some code",
			expectedIntent: IntentCoding,
			mockResponse:   `{"embedding": [0.9, 0.1]}`,
		},
		{
			input:          "what is life?",
			expectedIntent: IntentChat,
			mockResponse:   `{"embedding": [0.1, 0.9]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			mockResponses[tt.input] = tt.mockResponse
			intent, err := router.ClassifyIntent(&Request{Input: tt.input})
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if intent != tt.expectedIntent {
				t.Errorf("Expected %s, got %s", tt.expectedIntent, intent)
			}
		})
	}
}

func TestSmartRouter_SelectProvider(t *testing.T) {
	// Create a temporary prototypes file
	protoContent := `
intents:
  coding:
    - "generate code"
  chat:
    - "explain this"
providers:
  local:
    - "local task"
  cloud:
    - "cloud task"
`
	tmpFile, err := os.CreateTemp("", "prototypes*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.Write([]byte(protoContent)); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	mockLocal := &MockModelProvider{name: "LocalModel"}
	mockCloud := &MockModelProvider{name: "CloudModel"}

	// Mock responses for initialization
	mockResponses := map[string]string{
		"generate code": `{"embedding": [1.0, 0.0]}`,
		"explain this":  `{"embedding": [0.0, 1.0]}`,
		"local task":    `{"embedding": [1.0, 0.0]}`,
		"cloud task":    `{"embedding": [0.0, 1.0]}`,
	}

	mockClient := &http.Client{
		Transport: &MockRoundTripper{responses: mockResponses},
	}

	router, err := NewSmartRouter(mockLocal, mockCloud, "nomic-embed-text", mockClient, tmpFile.Name(), func(ctx context.Context, provider, model, apiKey string) (ModelProvider, error) {
		return &MockModelProvider{name: provider}, nil
	})
	if err != nil {
		t.Fatalf("Failed to create SmartRouter: %v", err)
	}

	tests := []struct {
		name               string
		input              string
		expectedModel      string
		mockOllamaResponse string
	}{
		{
			name:               "Close to local",
			input:              "do something local",
			expectedModel:      "LocalModel",
			mockOllamaResponse: `{"embedding": [0.9, 0.1]}`,
		},
		{
			name:               "Close to cloud",
			input:              "do something cloud",
			expectedModel:      "CloudModel",
			mockOllamaResponse: `{"embedding": [0.1, 0.9]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Update mock for the specific test case
			mockResponses[tt.input] = tt.mockOllamaResponse
			// Assume intent chat for these tests
			selectedProvider, err := router.SelectProvider(&Request{Input: tt.input}, IntentChat)

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if selectedProvider.Name() != tt.expectedModel {
				t.Errorf("Expected %s, got %s", tt.expectedModel, selectedProvider.Name())
			}
		})
	}
}