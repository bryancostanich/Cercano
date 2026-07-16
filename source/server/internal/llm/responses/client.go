package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"cercano/source/server/internal/inference"
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

// Client implements inference.Provider using the OpenAI Responses API.
type Client struct {
	http    httpx.Doer
	baseURL string
	apiKey  string
	model   string
	route   string
	tokens  TokenSource
	// tempUnsupported remembers models that rejected an explicit temperature
	// ("Unsupported parameter: temperature" — the gpt-5-family reasoning
	// models), so later calls skip the doomed attempt.
	tempUnsupported struct {
		mu sync.Mutex
		m  map[string]bool
	}
}

func (c *Client) tempUnsupportedLoad(k string) bool {
	c.tempUnsupported.mu.Lock()
	defer c.tempUnsupported.mu.Unlock()
	return c.tempUnsupported.m[k]
}

func (c *Client) tempUnsupportedStore(k string) {
	c.tempUnsupported.mu.Lock()
	defer c.tempUnsupported.mu.Unlock()
	if c.tempUnsupported.m == nil {
		c.tempUnsupported.m = map[string]bool{}
	}
	c.tempUnsupported.m[k] = true
}

// isTemperatureUnsupported matches the API rejection reasoning models return
// for an explicit temperature ("Unsupported parameter: temperature").
func isTemperatureUnsupported(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "temperature") &&
		(strings.Contains(msg, "unsupported") || strings.Contains(msg, "deprecated"))
}

// NewClient constructs a Client. There is no transport-level retry: retry
// policy is owned by the resilience engine above this adapter, where it is
// bounded, class-driven, and narrated to the user.
func NewClient(cfg Config) *Client {
	base := cfg.BaseURL
	if cfg.Route == RouteChatGPT {
		base = CodexBaseURL // the route pins the backend; ignore any profile BaseURL
	}
	if base == "" {
		base = defaultBaseURL
	}
	return &Client{http: &http.Client{}, baseURL: base, apiKey: cfg.APIKey, model: cfg.Model, route: cfg.Route, tokens: cfg.TokenSource}
}

func (c *Client) Name() string { return "openai-responses" }

func (c *Client) Capabilities() inference.Capabilities {
	return inference.Capabilities{
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
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// The request never produced an HTTP response: DNS, connection
		// refused/reset, TLS.
		return nil, &llm.Error{Class: llm.ErrNetwork, Provider: c.Name(), Err: err}
	}
	return resp, nil
}

// authorize sets the auth headers on a request. For the ChatGPT route it
// pulls a fresh (auto-refreshing) subscription bearer + account ID from the
// token source and identifies the client the way the codex backend expects;
// otherwise it uses the static API key.
func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	if c.tokens != nil {
		access, accountID, err := c.tokens.Token(ctx)
		if err != nil {
			// A failing token source (logged-out ChatGPT subscription, refresh
			// rejected) is an auth-class failure: the resilience engine fails
			// it over rather than retrying a credential that won't heal.
			return &llm.Error{Class: llm.ErrAuth, Provider: c.Name(),
				Err: fmt.Errorf("responses: chatgpt auth: %w", err)}
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

// errorFromBody turns a non-2xx response body into a readable error plus the
// wire error code/type when present. Two body shapes exist: api.openai.com
// wraps errors as {"error":{"message":...}}, while the ChatGPT codex backend
// returns bare {"detail":"..."}. Anything else falls back to the status plus
// a body snippet — a bare status code is undiagnosable.
func errorFromBody(status int, body []byte) (err error, code, typ string) {
	var env struct {
		Error  *apiError `json:"error"`
		Detail string    `json:"detail"`
	}
	if json.Unmarshal(body, &env) == nil {
		if env.Error != nil && env.Error.Message != "" {
			return fmt.Errorf("responses: %s", env.Error.Message), env.Error.Code, env.Error.Type
		}
		if env.Detail != "" {
			return fmt.Errorf("responses: %s", env.Detail), "", ""
		}
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	if snippet == "" {
		return fmt.Errorf("responses: status %d", status), "", ""
	}
	return fmt.Errorf("responses: status %d: %s", status, snippet), "", ""
}

// normalizeHTTP maps a non-2xx response into the provider-agnostic llm.Error
// taxonomy. Quota detection uses OpenAI's explicit insufficient_quota marker,
// usage-limit phrasing (the ChatGPT codex backend reports subscription caps in
// prose), or a quota-scale Retry-After.
func (c *Client) normalizeHTTP(resp *http.Response, body []byte) error {
	inner, code, typ := errorFromBody(resp.StatusCode, body)
	ne := &llm.Error{
		Provider:   c.Name(),
		StatusCode: resp.StatusCode,
		RetryAfter: httpx.RetryAfter(resp.Header),
		Err:        inner,
	}
	msg := strings.ToLower(inner.Error())
	quotaMarked := code == "insufficient_quota" || typ == "insufficient_quota" ||
		strings.Contains(msg, "quota") || strings.Contains(msg, "usage limit")
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		ne.Class = llm.ErrAuth
	case resp.StatusCode == http.StatusTooManyRequests:
		if quotaMarked || ne.RetryAfter >= httpx.QuotaRetryAfterFloor {
			ne.Class = llm.ErrQuota
		} else {
			ne.Class = llm.ErrBusy
		}
	case resp.StatusCode >= 500:
		ne.Class = llm.ErrBusy
	case resp.StatusCode >= 400:
		ne.Class = llm.ErrInvalidRequest
	default:
		ne.Class = llm.ErrUnknown
	}
	return ne
}

// Chat sends a non-streaming Responses request and maps output to blocks.
// Models that reject an explicit temperature (gpt-5-family reasoning models)
// get one retry without it — callers that request greedy decoding prefer a
// default-temperature completion over a failed call — and are remembered so
// later calls skip the doomed attempt.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if req.Temperature != nil && c.tempUnsupportedLoad(req.Model) {
		req.Temperature = nil
	}
	resp, err := c.chatOnce(ctx, req)
	if err != nil && req.Temperature != nil && isTemperatureUnsupported(err) {
		c.tempUnsupportedStore(req.Model)
		req.Temperature = nil
		resp, err = c.chatOnce(ctx, req)
	}
	return resp, err
}

func (c *Client) chatOnce(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	// The ChatGPT-account codex backend rejects non-streaming requests
	// ("Stream must be set to true"). For that route, run the streaming path and
	// aggregate it into the non-streaming ChatResponse shape.
	if c.route == RouteChatGPT {
		rdr, err := c.StreamChat(ctx, req)
		if err != nil {
			return llm.ChatResponse{}, err
		}
		defer rdr.Close()
		return llm.CollectStream(ctx, rdr, nil, nil)
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
		return llm.ChatResponse{}, c.normalizeHTTP(httpResp, body)
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return llm.ChatResponse{}, fmt.Errorf("responses: decode: %w", err)
	}
	out := llm.ChatResponse{Blocks: blocksFromOutput(r.Output), StopReason: r.Status, Model: r.Model}
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
		return nil, c.normalizeHTTP(httpResp, body)
	}
	return newStreamReader(httpResp.Body), nil
}
