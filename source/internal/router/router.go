package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

// Ollama API structs
type ollamaGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaGenerateResponse struct {
	Model  string `json:"model"`
	Response string `json:"response"`
	Done   bool   `json:"done"`
}

// Request represents a request to be processed by an AI model.
// This will be expanded later to include more context.
type Request struct {
	Input string
}

// Response represents a response from an AI model.
type Response struct {
	Output string
}

// ModelProvider defines the interface for an AI model provider (local or cloud).
type ModelProvider interface {
	// Process handles a request and returns a response.
	Process(ctx context.Context, req *Request) (*Response, error)
	// Name returns the name of the model provider.
	Name() string
}

// Router defines the interface for a smart router that selects a model provider.
type Router interface {
	// SelectProvider selects a model provider based on the request.
	SelectProvider(req *Request) (ModelProvider, error)
}

const (
	ollamaAPIURL       = "http://localhost:11434/api/generate"
	guidelinesFilePath = "../../router_guidelines.md" // Path relative to `source/internal/router`
)

// SmartRouter implements the Router interface with routing logic based on guidelines.
type SmartRouter struct {
	ModelProviders map[string]ModelProvider
	Guidelines     string // Stores the loaded router guidelines
	ModelName      string // Name of the local LLM model to use for classification
	httpClient     *http.Client // For making HTTP requests to Ollama API, testable
	queryFunc      func(prompt string) (string, error) // Function to query the LLM, for testability
}

// NewSmartRouter creates a new SmartRouter.
// It loads the routing guidelines from the specified Markdown file.
func NewSmartRouter(local, cloud ModelProvider, modelName string) (*SmartRouter, error) {
	guidelinesBytes, err := ioutil.ReadFile(guidelinesFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load router guidelines from %s: %w", guidelinesFilePath, err)
	}
	return &SmartRouter{
		ModelProviders: map[string]ModelProvider{
			"LocalModel": local,
			"CloudModel": cloud,
		},
		Guidelines: string(guidelinesBytes),
		ModelName:  modelName,
		httpClient: http.DefaultClient, // Use default client in production
	}, nil
}

// queryOllama sends a prompt to the local Ollama instance and returns its response.
func (sr *SmartRouter) queryOllama(prompt string) (string, error) {
	requestBody, err := json.Marshal(ollamaGenerateRequest{
		Model:  sr.ModelName,
		Prompt: prompt,
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal Ollama request: %w", err)
	}

	req, err := http.NewRequest("POST", ollamaAPIURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create new HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sr.httpClient.Do(req) // Use sr.httpClient
	if err != nil {
		return "", fmt.Errorf("failed to send request to Ollama API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("Ollama API returned non-OK status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Ollama API response body: %w", err)
	}

	var ollamaResp ollamaGenerateResponse
	if err := json.Unmarshal(responseBody, &ollamaResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal Ollama API response: %w", err)
	}

	return strings.TrimSpace(ollamaResp.Response), nil
}

// SelectProvider implements the smart routing algorithm using the local LLM.
func (sr *SmartRouter) SelectProvider(req *Request) (ModelProvider, error) {
	prompt := fmt.Sprintf("%s\n\nUser Request: %s\n\nClassify this request as either 'LocalModel' or 'CloudModel'. Output only the classification word.", sr.Guidelines, req.Input)

	classification, err := sr.queryOllama(prompt) // Call the method
	if err != nil {
		return nil, fmt.Errorf("failed to query Ollama for classification: %w", err)
	}

	switch classification {
	case "LocalModel":
		if provider, ok := sr.ModelProviders["LocalModel"]; ok {
			return provider, nil
		}
		return nil, fmt.Errorf("LocalModel provider not found")
	case "CloudModel":
		if provider, ok := sr.ModelProviders["CloudModel"]; ok {
			return provider, nil
		}
		return nil, fmt.Errorf("CloudModel provider not found")
	default:
		return nil, fmt.Errorf("unknown model classification: %s", classification)
	}
}