package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/httpx"
)

const defaultBaseURL = "https://api.openai.com/v1"

// RouteChatGPT is the profile Route value selecting ChatGPT subscription
// auth: requests go to the codex backend using an OAuth bearer token plus a
// ChatGPT-Account-Id header, rather than a static API key against
// api.openai.com.
const RouteChatGPT = "chatgpt"

// CodexBaseURL is the ChatGPT subscription backend base. The client appends
// "/responses" as usual. Used whenever Route == RouteChatGPT, regardless of
// any BaseURL configured on the profile.
const CodexBaseURL = "https://chatgpt.com/backend-api/codex"

// TokenSource supplies a valid subscription bearer token and ChatGPT account
// ID for each request, refreshing transparently. chatgptauth.Source
// implements it.
type TokenSource interface {
	Token(ctx context.Context) (access, accountID string, err error)
}

// Config holds the Responses client configuration.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	// Route selects the auth path. Empty (default) uses APIKey against
	// BaseURL; RouteChatGPT uses TokenSource against the codex backend.
	Route string
	// TokenSource, when set, supplies refreshing subscription bearers and is
	// used instead of APIKey. Required for RouteChatGPT.
	TokenSource TokenSource
}

// Client implements llm.Provider using the OpenAI Responses API.
type Client struct {
	http    httpx.Doer
	baseURL string
	apiKey  string
	model   string
	route   string
	tokens  TokenSource
}

// NewClient constructs a Client. The HTTP transport retries transient statuses
// (429/5xx) with backoff, shared with the chat client via internal/llm/httpx.
func NewClient(cfg Config) *Client {
	base := cfg.BaseURL
	if cfg.Route == RouteChatGPT {
		base = CodexBaseURL // the route pins the backend; ignore any profile BaseURL
	}
	if base == "" {
		base = defaultBaseURL
	}
	retry := &httpx.RetryTransport{
		Next:   &http.Client{},
		Policy: httpx.RetryPolicy{MaxAttempts: 3, BaseDelay: 500 * time.Millisecond, OnStatus: []int{429, 500, 502, 503}},
	}
	return &Client{http: retry, baseURL: base, apiKey: cfg.APIKey, model: cfg.Model, route: cfg.Route, tokens: cfg.TokenSource}
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
	// The ChatGPT codex backend rejects max_output_tokens ("Unsupported
	// parameter"); only the API-key path forwards it.
	if req.MaxTokens > 0 && c.route != RouteChatGPT {
		r.MaxOutputTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		tmp := *req.Temperature
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
	if err := c.authorize(ctx, httpReq); err != nil {
		return nil, err
	}
	return c.http.Do(httpReq)
}

// authorize sets the auth headers on a request. For the ChatGPT route it
// pulls a fresh (auto-refreshing) subscription bearer + account ID from the
// token source and identifies the client the way the codex backend expects;
// otherwise it uses the static API key.
func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	if c.tokens != nil {
		access, accountID, err := c.tokens.Token(ctx)
		if err != nil {
			return fmt.Errorf("responses: chatgpt auth: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+access)
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
		req.Header.Set("originator", "cercano")
		req.Header.Set("User-Agent", "cercano")
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	return nil
}

// errorFromBody turns a non-2xx response into a readable error. Two body
// shapes exist: api.openai.com wraps errors as {"error":{"message":...}},
// while the ChatGPT codex backend returns bare {"detail":"..."}. Anything
// else falls back to the status plus a body snippet — a bare status code is
// undiagnosable.
func errorFromBody(status int, body []byte) error {
	var env struct {
		Error  *apiError `json:"error"`
		Detail string    `json:"detail"`
	}
	if json.Unmarshal(body, &env) == nil {
		if env.Error != nil && env.Error.Message != "" {
			return fmt.Errorf("responses: %s", env.Error.Message)
		}
		if env.Detail != "" {
			return fmt.Errorf("responses: %s", env.Detail)
		}
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	if snippet == "" {
		return fmt.Errorf("responses: status %d", status)
	}
	return fmt.Errorf("responses: status %d: %s", status, snippet)
}

// Chat sends a non-streaming Responses request and maps output to blocks.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	// The ChatGPT-account codex backend rejects non-streaming requests
	// ("Stream must be set to true"). For that route, run the streaming path and
	// aggregate it into the non-streaming ChatResponse shape.
	if c.route == RouteChatGPT {
		rdr, err := c.StreamChat(ctx, req)
		if err != nil {
			return llm.ChatResponse{}, err
		}
		defer rdr.Close()
		return llm.CollectStream(ctx, rdr, nil)
	}
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

// StreamChat opens a streaming Responses request and returns a StreamReader that
// emits llm.StreamEvents.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	httpResp, err := c.do(ctx, c.buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, errorFromBody(httpResp.StatusCode, body)
	}
	return newStreamReader(httpResp.Body), nil
}
