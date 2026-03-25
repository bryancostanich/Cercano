package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/engine"
)

// OllamaEngine implements InferenceEngine and EmbeddingService using the Ollama HTTP API.
type OllamaEngine struct {
	mu            sync.RWMutex
	Client        *http.Client
	BaseURL       string
	fallbackURL   string
	activeURL     string
	usingFallback bool
}

// NewOllamaEngine creates a new OllamaEngine.
func NewOllamaEngine(baseURL string) *OllamaEngine {
	return &OllamaEngine{
		Client:      http.DefaultClient,
		BaseURL:     baseURL,
		fallbackURL: baseURL,
		activeURL:   baseURL,
	}
}

// Name returns the engine's name.
func (e *OllamaEngine) Name() string {
	return "ollama"
}

// SetBaseURL updates the Ollama endpoint URL at runtime.
func (e *OllamaEngine) SetBaseURL(url string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.BaseURL = url
	e.activeURL = url
	e.usingFallback = false
}

// GetActiveURL returns the URL currently being used for requests.
func (e *OllamaEngine) GetActiveURL() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeURL
}

func (e *OllamaEngine) SwitchToFallback() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeURL = e.fallbackURL
	e.usingFallback = true
}

func (e *OllamaEngine) SwitchToPrimary() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeURL = e.BaseURL
	e.usingFallback = false
}

func (e *OllamaEngine) IsUsingFallback() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.usingFallback
}

func (e *OllamaEngine) StartHealthMonitor(ctx context.Context, interval time.Duration, failureThreshold int) {
	e.mu.RLock()
	primary := e.BaseURL
	fallback := e.fallbackURL
	e.mu.RUnlock()

	if primary == fallback {
		return
	}

	go func() {
		consecutiveFailures := 0
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.pingOllama(ctx, primary) {
					consecutiveFailures = 0
					if e.IsUsingFallback() {
						log.Printf("HealthMonitor: primary endpoint %s recovered, switching back", primary)
						e.SwitchToPrimary()
					}
				} else {
					consecutiveFailures++
					if consecutiveFailures >= failureThreshold && !e.IsUsingFallback() {
						log.Printf("HealthMonitor: primary endpoint %s unreachable (%d failures), switching to fallback %s",
							primary, consecutiveFailures, fallback)
						e.SwitchToFallback()
					}
				}
			}
		}
	}()
}

func (e *OllamaEngine) pingOllama(ctx context.Context, baseURL string) bool {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(pingCtx, "GET", baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type tagsResponse struct {
	Models []engine.ModelInfo `json:"models"`
}

func (e *OllamaEngine) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	url := fmt.Sprintf("%s/api/tags", e.GetActiveURL())
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(body))
	}

	var tags tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	return tags.Models, nil
}

type generateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	System  string                 `json:"system,omitempty"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options"`
}

type generateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func (e *OllamaEngine) Complete(ctx context.Context, model, prompt, systemPrompt string) (string, error) {
	url := fmt.Sprintf("%s/api/generate", e.GetActiveURL())
	payload := generateRequest{
		Model:   model,
		Prompt:  prompt,
		System:  systemPrompt,
		Stream:  false,
		Options: map[string]interface{}{"num_ctx": 32768},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama error: %s", string(b))
	}
	var genResp generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return "", err
	}
	return genResp.Response, nil
}

func (e *OllamaEngine) CompleteStream(ctx context.Context, model, prompt, systemPrompt string, onToken func(string)) (string, error) {
	url := fmt.Sprintf("%s/api/generate", e.GetActiveURL())
	payload := generateRequest{
		Model:   model,
		Prompt:  prompt,
		System:  systemPrompt,
		Stream:  true,
		Options: map[string]interface{}{"num_ctx": 32768},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama error: %s", string(b))
	}
	var accumulated strings.Builder
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var chunk generateResponse
		if err := decoder.Decode(&chunk); err != nil {
			return "", err
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
	return accumulated.String(), nil
}

type ollamaEmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

func (e *OllamaEngine) Embed(ctx context.Context, model, text string) ([]float64, error) {
	url := fmt.Sprintf("%s/api/embeddings", e.GetActiveURL())
	payload := ollamaEmbeddingRequest{
		Model:  model,
		Prompt: text,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error: %s", string(b))
	}
	var embResp ollamaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, err
	}
	return embResp.Embedding, nil
}
