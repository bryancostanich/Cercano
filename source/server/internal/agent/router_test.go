package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
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

// TestClassifyIntent_CategoryScoring_ChatWins verifies that top-K average scoring
// correctly classifies "tell me about this class" as chat, even though single
// nearest-neighbor would pick coding (due to one exact-match coding prototype).
func TestClassifyIntent_CategoryScoring_ChatWins(t *testing.T) {
	protoContent := `
intents:
  coding:
    - "write a class"
    - "implement function"
    - "create module"
  chat:
    - "explain this code"
    - "what does this do"
    - "summarize this class"
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

	// Embeddings crafted so that:
	// - Single nearest-neighbor picks coding ("write a class" is exact match at sim=1.0)
	// - Top-3 average picks chat (consistently high ~0.99 vs coding's 1.0+0+0 / 3 = 0.33)
	mockResponses := map[string]string{
		// Coding prototypes: one exact match, two orthogonal
		"write a class":      `{"embedding": [1.0, 1.0, 0.0]}`,
		"implement function": `{"embedding": [1.0, -1.0, 0.0]}`,
		"create module":      `{"embedding": [0.0, 0.0, 1.0]}`,
		// Chat prototypes: all consistently close to query direction [1,1,0]
		"explain this code":  `{"embedding": [0.9, 1.0, 0.1]}`,
		"what does this do":  `{"embedding": [1.0, 0.9, 0.1]}`,
		"summarize this class": `{"embedding": [0.95, 0.95, 0.1]}`,
		// Provider prototypes (needed for initialization)
		"local task": `{"embedding": [1.0, 0.0, 0.0]}`,
		"cloud task": `{"embedding": [0.0, 1.0, 0.0]}`,
		// Query: exact same direction as "write a class" → single-NN picks coding
		"tell me about this class": `{"embedding": [1.0, 1.0, 0.0]}`,
	}
	mockClient := &http.Client{
		Transport: &MockRoundTripper{responses: mockResponses},
	}

	router, err := NewSmartRouter(nil, nil, "nomic-embed-text", mockClient, tmpFile.Name(), nil)
	if err != nil {
		t.Fatalf("Failed to create SmartRouter: %v", err)
	}

	intent, err := router.ClassifyIntent(&Request{Input: "tell me about this class"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if intent != IntentChat {
		t.Errorf("Expected IntentChat (top-K should favor chat), got %s", intent)
	}
}

// TestClassifyIntent_CategoryScoring_CodingStillWorks verifies that clear coding
// queries still classify as coding with top-K scoring.
func TestClassifyIntent_CategoryScoring_CodingStillWorks(t *testing.T) {
	protoContent := `
intents:
  coding:
    - "write code"
    - "generate function"
    - "create class"
  chat:
    - "hello there"
    - "what is life"
    - "tell me a joke"
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

	// All coding prototypes cluster near [1,0,0], chat near [0,1,0]
	mockResponses := map[string]string{
		"write code":       `{"embedding": [0.95, 0.05, 0.0]}`,
		"generate function": `{"embedding": [0.9, 0.1, 0.0]}`,
		"create class":     `{"embedding": [0.85, 0.15, 0.0]}`,
		"hello there":      `{"embedding": [0.05, 0.95, 0.0]}`,
		"what is life":     `{"embedding": [0.1, 0.9, 0.0]}`,
		"tell me a joke":   `{"embedding": [0.15, 0.85, 0.0]}`,
		"local task":       `{"embedding": [1.0, 0.0, 0.0]}`,
		"cloud task":       `{"embedding": [0.0, 1.0, 0.0]}`,
		// Query is clearly in coding territory
		"implement a sorting algorithm": `{"embedding": [0.92, 0.08, 0.0]}`,
	}
	mockClient := &http.Client{
		Transport: &MockRoundTripper{responses: mockResponses},
	}

	router, err := NewSmartRouter(nil, nil, "nomic-embed-text", mockClient, tmpFile.Name(), nil)
	if err != nil {
		t.Fatalf("Failed to create SmartRouter: %v", err)
	}

	intent, err := router.ClassifyIntent(&Request{Input: "implement a sorting algorithm"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if intent != IntentCoding {
		t.Errorf("Expected IntentCoding for clear coding query, got %s", intent)
	}
}

// TestSelectProvider_CategoryScoring verifies that provider selection also uses
// top-K category scoring.
func TestSelectProvider_CategoryScoring(t *testing.T) {
	protoContent := `
intents:
  coding:
    - "code"
  chat:
    - "chat"
providers:
  local:
    - "simple question"
    - "quick lookup"
    - "basic info"
  cloud:
    - "complex analysis"
    - "detailed review"
    - "deep research"
`
	tmpFile, err := os.CreateTemp("", "prototypes*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write([]byte(protoContent))
	tmpFile.Close()

	mockLocal := &MockModelProvider{name: "LocalModel"}
	mockCloud := &MockModelProvider{name: "CloudModel"}

	// Local prototypes cluster near [1,0], cloud near [0,1]
	mockResponses := map[string]string{
		"code":             `{"embedding": [1.0, 0.0]}`,
		"chat":             `{"embedding": [0.0, 1.0]}`,
		"simple question":  `{"embedding": [0.9, 0.1]}`,
		"quick lookup":     `{"embedding": [0.85, 0.15]}`,
		"basic info":       `{"embedding": [0.8, 0.2]}`,
		"complex analysis": `{"embedding": [0.1, 0.9]}`,
		"detailed review":  `{"embedding": [0.15, 0.85]}`,
		"deep research":    `{"embedding": [0.2, 0.8]}`,
		// Query clearly in local territory
		"what time is it": `{"embedding": [0.88, 0.12]}`,
	}
	mockClient := &http.Client{
		Transport: &MockRoundTripper{responses: mockResponses},
	}

	router, err := NewSmartRouter(mockLocal, mockCloud, "nomic-embed-text", mockClient, tmpFile.Name(), nil)
	if err != nil {
		t.Fatalf("Failed to create SmartRouter: %v", err)
	}

	provider, err := router.SelectProvider(&Request{Input: "what time is it"}, IntentChat)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if provider.Name() != "LocalModel" {
		t.Errorf("Expected LocalModel, got %s", provider.Name())
	}
}

// TestTopKAveragePerCategory_Unit directly tests the helper function.
func TestTopKAveragePerCategory_Unit(t *testing.T) {
	query := []float64{1.0, 0.0}

	prototypes := []PrototypeEmbedding{
		{Category: "A", Embedding: []float64{1.0, 0.0}},  // sim = 1.0
		{Category: "A", Embedding: []float64{0.0, 1.0}},  // sim = 0.0
		{Category: "A", Embedding: []float64{-1.0, 0.0}}, // sim = -1.0
		{Category: "B", Embedding: []float64{0.9, 0.1}},  // sim ≈ 0.994
		{Category: "B", Embedding: []float64{0.8, 0.2}},  // sim ≈ 0.970
		{Category: "B", Embedding: []float64{0.7, 0.3}},  // sim ≈ 0.919
	}

	bestCat, bestAvg := topKAveragePerCategory(query, prototypes, 3)

	// A top-3 avg: (1.0 + 0.0 + (-1.0)) / 3 = 0.0
	// B top-3 avg: (~0.994 + ~0.970 + ~0.919) / 3 ≈ 0.961
	if bestCat != "B" {
		t.Errorf("Expected category B, got %s", bestCat)
	}
	if bestAvg < 0.9 {
		t.Errorf("Expected bestAvg > 0.9, got %.4f", bestAvg)
	}
}

// TestExtractQueryText verifies that file context is stripped from input.
func TestExtractQueryText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no context",
			input:    "tell me about this class",
			expected: "tell me about this class",
		},
		{
			name:     "with context",
			input:    "tell me about this class\n\n--- Context from calculator.go ---\npackage sandbox\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n--- End Context ---\n",
			expected: "tell me about this class",
		},
		{
			name:     "with context no trailing newline",
			input:    "tell me about this class\n\n--- Context from main.go ---\npackage main",
			expected: "tell me about this class",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractQueryText(tt.input)
			if got != tt.expected {
				t.Errorf("extractQueryText() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestClassifyIntent_WithFileContext verifies that appended file context
// does not skew intent classification.
func TestClassifyIntent_WithFileContext(t *testing.T) {
	protoContent := `
intents:
  coding:
    - "write a class"
    - "implement function"
    - "create module"
  chat:
    - "explain this code"
    - "what does this do"
    - "tell me about this"
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
		"write a class":      `{"embedding": [1.0, 0.0]}`,
		"implement function": `{"embedding": [0.9, 0.1]}`,
		"create module":      `{"embedding": [0.85, 0.15]}`,
		"explain this code":  `{"embedding": [0.0, 1.0]}`,
		"what does this do":  `{"embedding": [0.1, 0.9]}`,
		"tell me about this": `{"embedding": [0.05, 0.95]}`,
		"local task":         `{"embedding": [1.0, 0.0]}`,
		"cloud task":         `{"embedding": [0.0, 1.0]}`,
		// Query WITHOUT context — should be what gets embedded after stripping
		"tell me about this class": `{"embedding": [0.1, 0.9]}`,
	}
	mockClient := &http.Client{
		Transport: &MockRoundTripper{responses: mockResponses},
	}

	router, err := NewSmartRouter(nil, nil, "nomic-embed-text", mockClient, tmpFile.Name(), nil)
	if err != nil {
		t.Fatalf("Failed to create SmartRouter: %v", err)
	}

	// Input includes file context that would skew toward coding
	inputWithContext := "tell me about this class\n\n--- Context from calculator.go ---\npackage sandbox\n\nfunc Add(a, b int) int { return a + b }\n--- End Context ---\n"

	intent, err := router.ClassifyIntent(&Request{Input: inputWithContext})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if intent != IntentChat {
		t.Errorf("Expected IntentChat when context is stripped, got %s", intent)
	}
}

// TestTopKAveragePerCategory_KLargerThanCategory verifies graceful handling
// when K exceeds the number of prototypes in a category.
func TestTopKAveragePerCategory_KLargerThanCategory(t *testing.T) {
	query := []float64{1.0, 0.0}

	prototypes := []PrototypeEmbedding{
		{Category: "Small", Embedding: []float64{0.9, 0.1}}, // only 1 prototype
		{Category: "Large", Embedding: []float64{0.5, 0.5}},
		{Category: "Large", Embedding: []float64{0.6, 0.4}},
		{Category: "Large", Embedding: []float64{0.7, 0.3}},
	}

	bestCat, bestAvg := topKAveragePerCategory(query, prototypes, 5) // K=5 > category sizes

	// Small: 1 prototype, sim ≈ 0.994 → avg = 0.994
	// Large: 3 prototypes, avg of all 3
	//   [0.5,0.5] sim = 0.5/sqrt(0.5) = 0.7071
	//   [0.6,0.4] sim = 0.6/sqrt(0.52) ≈ 0.8321
	//   [0.7,0.3] sim = 0.7/sqrt(0.58) ≈ 0.9191
	//   avg ≈ 0.8194
	// Small wins
	if bestCat != "Small" {
		t.Errorf("Expected category Small, got %s", bestCat)
	}

	expectedSim := CosineSimilarity(query, []float64{0.9, 0.1})
	if math.Abs(bestAvg-expectedSim) > 0.001 {
		t.Errorf("Expected bestAvg ≈ %.4f, got %.4f", expectedSim, bestAvg)
	}
}