package anthropic

import (
	"context"
	"net/http"
	"strings"
	"sync"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

type Config struct {
	BaseURL   string
	APIKey    string
	Model     string
	UserAgent string
	// Route names the access path and selects the request authenticator
	// (see auth.go):
	//   "" / "direct"  → vanilla Anthropic API; the SDK's x-api-key stands.
	//   "subscription" → Claude Max/Pro OAuth: a refreshing Bearer token, the
	//                    oauth beta header, and the Claude Code identity system
	//                    block (auth_subscription.go). Requires TokenSource.
	//   "meridian"     → legacy OpenCode-spoof bridge (auth_meridian.go),
	//                    slated for deletion once the subscription route lands.
	Route string
	// TokenSource supplies refreshing subscription bearers; required when
	// Route == "subscription", ignored otherwise. anthropicauth.Source
	// satisfies it structurally.
	TokenSource TokenSource
}

type ChatRequest = llm.ChatRequest
type ChatResponse = llm.ChatResponse

type Client struct {
	cfg Config
	sdk *sdk.Client
	// systemPrefix is prepended as the first system block on every request.
	// Non-empty only on the subscription route (the Claude Code identity),
	// where Anthropic gates access on the leading system block.
	systemPrefix string
	// tempDeprecated remembers models that rejected an explicit temperature
	// ("`temperature` is deprecated for this model"), so later calls skip the
	// doomed attempt instead of paying a 400 round-trip each time.
	tempDeprecated modelSet
}

// modelSet is a small concurrency-safe string set.
type modelSet struct {
	mu sync.Mutex
	m  map[string]bool
}

func (s *modelSet) Load(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k]
}

func (s *modelSet) Store(k string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]bool{}
	}
	s.m[k] = true
}

func NewClient(cfg Config) *Client {
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "dummy"
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	opts = append(opts, option.WithHTTPClient(&http.Client{
		Transport: &authRoundTripper{
			base: http.DefaultTransport,
			ua:   cfg.UserAgent,
			auth: authenticatorForRoute(cfg),
		},
	}))
	c := sdk.NewClient(opts...)
	return &Client{cfg: cfg, sdk: &c, systemPrefix: systemPrefixForRoute(cfg.Route)}
}

func (c *Client) Name() string { return "anthropic" }

func (c *Client) Capabilities() inference.Capabilities {
	return inference.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: true,
		SupportsCaching:       true,
		SupportsVision:        true,
		MaxToolsPerCall:       0,
	}
}

// systemBlocks assembles the system prompt as ordered text blocks. The prefix
// (the Claude Code identity, on the subscription route) must come first —
// Anthropic gates subscription access on the leading system block.
func systemBlocks(prefix, system string) []sdk.TextBlockParam {
	var out []sdk.TextBlockParam
	if prefix != "" {
		out = append(out, sdk.TextBlockParam{Text: prefix})
	}
	if system != "" {
		out = append(out, sdk.TextBlockParam{Text: system})
	}
	return out
}

// defaultMaxTokens floors an unset ChatRequest.MaxTokens. max_tokens:0 is not
// rejected by api.anthropic.com — the completion returns with zero output
// tokens and no error, so a forgotten budget becomes silent empty text (the
// 2026-07-15 empty-compaction-summary incident). Never send 0.
const defaultMaxTokens = 4096

func (c *Client) buildParams(req ChatRequest) sdk.MessageNewParams {
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: maxTokens,
		Messages:  messagesToSDK(req.Messages),
	}
	if sys := systemBlocks(c.systemPrefix, req.System); len(sys) > 0 {
		params.System = sys
	}
	if len(req.Tools) > 0 {
		params.Tools = toolsToSDK(req.Tools)
	}
	if req.Temperature != nil {
		params.Temperature = sdk.Float(*req.Temperature)
	}
	return params
}

// isTemperatureDeprecated matches the API rejection newer models return for
// an explicit temperature ("`temperature` is deprecated for this model.").
func isTemperatureDeprecated(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "temperature") && strings.Contains(msg, "deprecated")
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// Some models reject the temperature parameter outright — greedy decoding
	// is unattainable there, and callers that request it (the compaction
	// summarizer) prefer a default-temperature completion over a failed call.
	// Skip the doomed attempt for models we've already seen reject it; on a
	// fresh rejection, retry once without and remember.
	if req.Temperature != nil && c.tempDeprecated.Load(req.Model) {
		req.Temperature = nil
	}
	resp, err := c.sdk.Messages.New(ctx, c.buildParams(req))
	if err != nil && req.Temperature != nil && isTemperatureDeprecated(err) {
		c.tempDeprecated.Store(req.Model)
		req.Temperature = nil
		resp, err = c.sdk.Messages.New(ctx, c.buildParams(req))
	}
	if err != nil {
		return ChatResponse{}, err
	}
	out := ChatResponse{
		StopReason:   string(resp.StopReason),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
		Model:        string(resp.Model),
	}
	for _, b := range resp.Content {
		out.Blocks = append(out.Blocks, blockFromSDK(b))
	}
	return out, nil
}

func (c *Client) StreamChat(ctx context.Context, req ChatRequest) (llm.StreamReader, error) {
	st := c.sdk.Messages.NewStreaming(ctx, c.buildParams(req))
	return &streamReader{stream: st, blockKind: map[int64]string{}}, nil
}
