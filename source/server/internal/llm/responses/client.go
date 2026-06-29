package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/httpx"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Config holds the Responses client configuration.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Client implements llm.Provider using the OpenAI Responses API.
type Client struct {
	http    httpx.Doer
	baseURL string
	apiKey  string
	model   string
}

// NewClient constructs a Client. The HTTP transport retries transient statuses
// (429/5xx) with backoff, shared with the chat client via internal/llm/httpx.
func NewClient(cfg Config) *Client {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	retry := &httpx.RetryTransport{
		Next:   &http.Client{},
		Policy: httpx.RetryPolicy{MaxAttempts: 3, BaseDelay: 500 * time.Millisecond, OnStatus: []int{429, 500, 502, 503}},
	}
	return &Client{http: retry, baseURL: base, apiKey: cfg.APIKey, model: cfg.Model}
}

func (c *Client) Name() string { return "openai-responses" }

func (c *Client) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: true,
		SupportsCaching:       false,
		SupportsVision:        true,
	}
}

func modelOr(def, override string) string {
	if override != "" {
		return override
	}
	return def
}

func (c *Client) buildRequest(req llm.ChatRequest, stream bool) request {
	r := request{
		Model:        modelOr(c.model, req.Model),
		Instructions: req.System,
		Input:        messagesToInput(req.Messages),
		Tools:        toolsToResponses(req.Tools),
		Store:        false,
		Include:      []string{"reasoning.encrypted_content"},
		Stream:       stream,
	}
	if req.MaxTokens > 0 {
		r.MaxOutputTokens = req.MaxTokens
	}
	if req.Temperature > 0 {
		tmp := req.Temperature
		r.Temperature = &tmp
	}
	return r
}

// do POSTs the request body to /responses and returns the raw http response.
func (c *Client) do(ctx context.Context, body request) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	return c.http.Do(httpReq)
}

// errorFromBody turns a non-2xx response into a readable error.
func errorFromBody(status int, body []byte) error {
	var env struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && env.Error != nil && env.Error.Message != "" {
		return fmt.Errorf("responses: %s", env.Error.Message)
	}
	return fmt.Errorf("responses: status %d", status)
}

// Chat sends a non-streaming Responses request and maps output to blocks.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	httpResp, err := c.do(ctx, c.buildRequest(req, false))
	if err != nil {
		return llm.ChatResponse{}, err
	}
	defer httpResp.Body.Close()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return llm.ChatResponse{}, errorFromBody(httpResp.StatusCode, body)
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return llm.ChatResponse{}, fmt.Errorf("responses: decode: %w", err)
	}
	out := llm.ChatResponse{Blocks: blocksFromOutput(r.Output), StopReason: r.Status}
	if r.Usage != nil {
		out.InputTokens = r.Usage.InputTokens
		out.OutputTokens = r.Usage.OutputTokens
	}
	return out, nil
}

// StreamChat is implemented in stream.go (Task 5). Placeholder kept minimal so the
// package compiles; replaced in the next task.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, fmt.Errorf("responses: streaming not yet implemented")
}
