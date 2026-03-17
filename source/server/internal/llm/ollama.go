package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"

	"cercano/source/server/internal/agent"
)

type OllamaProvider struct {
	mu          sync.RWMutex
	ModelName   string
	BaseURL     string // primary URL (what the user configured)
	fallbackURL string // always the initial local URL
	activeURL   string // the URL currently being used for requests
	usingFallback bool
	Client      *http.Client
}

func NewOllamaProvider(modelName, baseURL string) *OllamaProvider {
	return &OllamaProvider{
		ModelName:   modelName,
		BaseURL:     baseURL,
		fallbackURL: baseURL,
		activeURL:   baseURL,
		Client:      http.DefaultClient,
	}
}

// SetModelName updates the model name at runtime (thread-safe).
func (p *OllamaProvider) SetModelName(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ModelName = name
}

func (p *OllamaProvider) Name() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModelName
}

// SetBaseURL updates the Ollama endpoint URL at runtime (thread-safe).
// When a remote URL is set, it becomes the primary and the original local URL
// becomes the fallback. The active URL is set to the new primary.
func (p *OllamaProvider) SetBaseURL(url string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.BaseURL = url
	p.activeURL = url
	p.usingFallback = false
}

// GetBaseURL returns the primary (configured) Ollama endpoint URL (thread-safe).
func (p *OllamaProvider) GetBaseURL() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.BaseURL
}

// GetActiveURL returns the URL currently being used for requests (thread-safe).
func (p *OllamaProvider) GetActiveURL() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeURL
}

// IsUsingFallback returns true if requests are currently routed to the fallback URL.
func (p *OllamaProvider) IsUsingFallback() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.usingFallback
}

// SwitchToFallback routes all requests to the fallback (local) URL.
func (p *OllamaProvider) SwitchToFallback() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeURL = p.fallbackURL
	p.usingFallback = true
}

// SwitchToPrimary routes all requests back to the primary URL.
func (p *OllamaProvider) SwitchToPrimary() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeURL = p.BaseURL
	p.usingFallback = false
}

// ModelInfo represents a model available on the Ollama instance.
type ModelInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

type tagsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ListModels queries the Ollama instance for available models via GET /api/tags.
func (p *OllamaProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	p.mu.RLock()
	activeURL := p.activeURL
	p.mu.RUnlock()

	url := fmt.Sprintf("%s/api/tags", activeURL)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tags tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return tags.Models, nil
}

type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func (p *OllamaProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	p.mu.RLock()
	modelName := p.ModelName
	activeURL := p.activeURL
	p.mu.RUnlock()

	url := fmt.Sprintf("%s/api/generate", activeURL)

	payload := generateRequest{
		Model:  modelName,
		Prompt: req.Input,
		Stream: false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error: %s", string(respBody))
	}

	var genResp generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &agent.Response{Output: genResp.Response}, nil
}

// ProcessStream sends a streaming request to Ollama and calls onToken for each chunk.
// Returns the complete accumulated response when done.
func (p *OllamaProvider) ProcessStream(ctx context.Context, req *agent.Request, onToken agent.TokenFunc) (*agent.Response, error) {
	p.mu.RLock()
	modelName := p.ModelName
	activeURL := p.activeURL
	p.mu.RUnlock()

	url := fmt.Sprintf("%s/api/generate", activeURL)

	payload := generateRequest{
		Model:  modelName,
		Prompt: req.Input,
		Stream: true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error: %s", string(respBody))
	}

	var accumulated strings.Builder
	decoder := json.NewDecoder(resp.Body)

	for decoder.More() {
		var chunk generateResponse
		if err := decoder.Decode(&chunk); err != nil {
			return nil, fmt.Errorf("failed to decode stream chunk: %w", err)
		}
		if chunk.Response != "" {
			accumulated.WriteString(chunk.Response)
			if onToken != nil {
				onToken(chunk.Response)
			}
		}
		if chunk.Done {
			break
		}
	}

	return &agent.Response{Output: accumulated.String()}, nil
}
