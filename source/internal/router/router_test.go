package router

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
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
	Response *http.Response
	Err      error
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Response, nil
}

// newMockClient creates a new mock http.Client that returns the given response or error.
func newMockClient(statusCode int, body string, err error) *http.Client {
	return &http.Client{
		Transport: &MockRoundTripper{
			Response: &http.Response{
				StatusCode: statusCode,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			},
			Err: err,
		},
	}
}

func TestSmartRouter_SelectProvider(t *testing.T) {
	mockLocal := &MockModelProvider{name: "LocalModel"}
	mockCloud := &MockModelProvider{name: "CloudModel"}

	router, err := NewSmartRouter(mockLocal, mockCloud, "phi") // Updated NewSmartRouter call
	if err != nil {
		t.Fatalf("Failed to create SmartRouter: %v", err)
	}

	tests := []struct {
		name               string
		input              string
		expectedModel      string
		mockOllamaResponse string // New field for mock Ollama response
		expectError        bool
	}{
		{
			name:               "File system operation, should go to local",
			input:              "Move the file 'main.go' to 'cmd/app/'",
			expectedModel:      "LocalModel",
			mockOllamaResponse: `{"model":"phi","response":"LocalModel","done":true}`, // Simulate Ollama response
			expectError:        false,
		},
		{
			name:               "Code explanation, should go to local",
			input:              "What does this function do? func (s *Server) foo() { ... }",
			expectedModel:      "LocalModel",
			mockOllamaResponse: `{"model":"phi","response":"LocalModel","done":true}`,
			expectError:        false,
		},
		{
			name:               "Architectural analysis, should go to cloud",
			input:              "Summarize the project's architecture.",
			expectedModel:      "CloudModel",
			mockOllamaResponse: `{"model":"phi","response":"CloudModel","done":true}`,
			expectError:        false,
		},
		{
			name:               "General knowledge, should go to cloud",
			input:              "What is the capital of France?",
			expectedModel:      "CloudModel",
			mockOllamaResponse: `{"model":"phi","response":"CloudModel","done":true}`,
			expectError:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router.httpClient = newMockClient(http.StatusOK, tt.mockOllamaResponse, nil) // Assign mock client
			selectedProvider, err := router.SelectProvider(&Request{Input: tt.input})

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected an error, but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if selectedProvider.Name() != tt.expectedModel {
				t.Errorf("Expected model %s, but got %s", tt.expectedModel, selectedProvider.Name())
			}
		})
	}
}
