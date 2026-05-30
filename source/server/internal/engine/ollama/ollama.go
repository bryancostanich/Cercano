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

// SwitchToFallback updates the active Ollama URL to the fallback endpoint and marks the engine as using the fallback.
func (e *OllamaEngine) SwitchToFallback() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeURL = e.fallbackURL
	e.usingFallback = true
}

// SwitchToPrimary switches the Ollama engine to use the primary endpoint and disables fallback mode.
func (e *OllamaEngine) SwitchToPrimary() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeURL = e.BaseURL
	e.usingFallback = false
}

// IsUsingFallback returns whether the Ollama engine is currently using a fallback local endpoint.
func (e *OllamaEngine) IsUsingFallback() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.usingFallback
}

// StartHealthMonitor starts a background goroutine to monitor the health of the primary Ollama endpoint and switches to the fallback endpoint if failures exceed the threshold.
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

// ListModels retrieves the list of available models from the Ollama instance.
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
	Response       string `json:"response"`
	Done           bool   `json:"done"`
	PromptEvalCount int   `json:"prompt_eval_count"`
	EvalCount       int   `json:"eval_count"`
}

// Complete generates a response using the Ollama inference engine with the given model, prompt, and system prompt.
func (e *OllamaEngine) Complete(ctx context.Context, model, prompt, systemPrompt string) (engine.CompletionResult, error) {
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
		return engine.CompletionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return engine.CompletionResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return engine.CompletionResult{}, fmt.Errorf("ollama error: %s", string(b))
	}
	var genResp generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return engine.CompletionResult{}, err
	}
	return engine.CompletionResult{
		Output:       genResp.Response,
		InputTokens:  genResp.PromptEvalCount,
		OutputTokens: genResp.EvalCount,
	}, nil
}

// CompleteStream sends a streaming generate request to the Ollama API and returns the accumulated response, invoking onToken for each received chunk.
func (e *OllamaEngine) CompleteStream(ctx context.Context, model, prompt, systemPrompt string, onToken func(string)) (engine.CompletionResult, error) {
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
		return engine.CompletionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return engine.CompletionResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return engine.CompletionResult{}, fmt.Errorf("ollama error: %s", string(b))
	}
	var accumulated strings.Builder
	var lastChunk generateResponse
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var chunk generateResponse
		if err := decoder.Decode(&chunk); err != nil {
			return engine.CompletionResult{}, err
		}
		if chunk.Response != "" {
			accumulated.WriteString(chunk.Response)
			if onToken != nil {
				onToken(chunk.Response)
			}
		}
		if chunk.Done {
			lastChunk = chunk
			break
		}
	}
	return engine.CompletionResult{
		Output:       accumulated.String(),
		InputTokens:  lastChunk.PromptEvalCount,
		OutputTokens: lastChunk.EvalCount,
	}, nil
}

type ollamaEmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

// Embed returns the embedding vector for the given text using the specified Ollama model.
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

// ChatWithTools sends a tool-use-capable chat request to Ollama's /api/chat
// endpoint with streaming enabled, accumulates the response, and returns the
// final assistant message (text and/or tool_calls).
//
// Diagnostic logging is written to stderr ("ollama-chat: " prefix) so the
// dispatch hang can be characterized by re-running and inspecting logs.
func (e *OllamaEngine) ChatWithTools(ctx context.Context, req engine.ChatRequest) (engine.ChatResponse, error) {
	url := fmt.Sprintf("%s/api/chat", e.GetActiveURL())
	payload := map[string]interface{}{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   true,
		"options":  map[string]interface{}{"num_ctx": 32768},
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	body, _ := json.Marshal(payload)

	log.Printf("ollama-chat: POST %s model=%q messages=%d tools=%d payload_bytes=%d",
		url, req.Model, len(req.Messages), len(req.Tools), len(body))
	// Dump payload (truncated) so we can see exactly what Ollama receives.
	preview := string(body)
	if len(preview) > 4000 {
		preview = preview[:4000] + "...[truncated]"
	}
	log.Printf("ollama-chat: payload=%s", preview)

	startReq := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return engine.ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(httpReq)
	if err != nil {
		log.Printf("ollama-chat: HTTP error after %s: %v", time.Since(startReq), err)
		return engine.ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("ollama-chat: HTTP %d after %s: %s", resp.StatusCode, time.Since(startReq), string(b))
		return engine.ChatResponse{}, fmt.Errorf("ollama chat error: %s", string(b))
	}
	log.Printf("ollama-chat: response headers received in %s, decoding stream", time.Since(startReq))

	type chunkType struct {
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		Done            bool `json:"done"`
		PromptEvalCount int  `json:"prompt_eval_count"`
		EvalCount       int  `json:"eval_count"`
	}

	var (
		accumulatedContent strings.Builder
		finalToolCalls     []engine.ToolCall
		promptEvalCount    int
		evalCount          int
		chunkN             int
		lastChunkAt        = time.Now()
	)
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var chunk chunkType
		if err := decoder.Decode(&chunk); err != nil {
			log.Printf("ollama-chat: decode error after %d chunks (%s): %v",
				chunkN, time.Since(startReq), err)
			return engine.ChatResponse{}, err
		}
		chunkN++
		gap := time.Since(lastChunkAt)
		lastChunkAt = time.Now()
		contentPreview := chunk.Message.Content
		if len(contentPreview) > 200 {
			contentPreview = contentPreview[:200] + "..."
		}
		log.Printf("ollama-chat: chunk %d (+%s since prev) content_len=%d tool_calls=%d done=%v content_preview=%q",
			chunkN, gap, len(chunk.Message.Content), len(chunk.Message.ToolCalls), chunk.Done, contentPreview)
		if chunk.Message.Content != "" {
			accumulatedContent.WriteString(chunk.Message.Content)
		}
		// Tool calls typically arrive complete in a single chunk (Ollama buffers them).
		// If multiple chunks carry tool_calls, take the last non-empty set.
		if len(chunk.Message.ToolCalls) > 0 {
			finalToolCalls = finalToolCalls[:0]
			for i, tc := range chunk.Message.ToolCalls {
				id := tc.ID
				if id == "" {
					id = fmt.Sprintf("tc_%d", i)
				}
				args := tc.Function.Arguments
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				finalToolCalls = append(finalToolCalls, engine.ToolCall{
					ID: id,
					Function: engine.ToolCallFunc{
						Name:      tc.Function.Name,
						Arguments: args,
					},
				})
			}
		}
		if chunk.Done {
			promptEvalCount = chunk.PromptEvalCount
			evalCount = chunk.EvalCount
		}
	}

	log.Printf("ollama-chat: stream complete after %s, chunks=%d, content_len=%d, tool_calls=%d, prompt_eval=%d, eval=%d",
		time.Since(startReq), chunkN, accumulatedContent.Len(), len(finalToolCalls), promptEvalCount, evalCount)

	return engine.ChatResponse{
		Content:      accumulatedContent.String(),
		ToolCalls:    finalToolCalls,
		InputTokens:  promptEvalCount,
		OutputTokens: evalCount,
	}, nil
}
