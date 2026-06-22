package anthropic

import (
	"context"
	"net/http"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"cercano/source/server/internal/llm"
)

type Config struct {
	BaseURL   string
	APIKey    string
	Model     string
	UserAgent string
}

type ChatRequest = llm.ChatRequest
type ChatResponse = llm.ChatResponse

type Client struct {
	cfg Config
	sdk *sdk.Client
}

type uaRoundTripper struct {
	base http.RoundTripper
	ua   string
}

func (u *uaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if u.ua != "" {
		r.Header.Set("User-Agent", u.ua)
	}
	return u.base.RoundTrip(r)
}

func NewClient(cfg Config) *Client {
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "dummy"
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.UserAgent != "" {
		opts = append(opts, option.WithHTTPClient(&http.Client{
			Transport: &uaRoundTripper{base: http.DefaultTransport, ua: cfg.UserAgent},
		}))
	}
	c := sdk.NewClient(opts...)
	return &Client{cfg: cfg, sdk: &c}
}

func (c *Client) Name() string { return "anthropic" }

func (c *Client) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: true,
		SupportsCaching:       true,
		SupportsVision:        true,
		MaxToolsPerCall:       0,
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  messagesToSDK(req.Messages),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	if len(req.Tools) > 0 {
		params.Tools = toolsToSDK(req.Tools)
	}
	if req.Temperature > 0 {
		params.Temperature = sdk.Float(req.Temperature)
	}

	resp, err := c.sdk.Messages.New(ctx, params)
	if err != nil {
		return ChatResponse{}, err
	}
	out := ChatResponse{
		StopReason:   string(resp.StopReason),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
	}
	for _, b := range resp.Content {
		out.Blocks = append(out.Blocks, blockFromSDK(b))
	}
	return out, nil
}

func (c *Client) StreamChat(ctx context.Context, req ChatRequest) (llm.StreamReader, error) {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  messagesToSDK(req.Messages),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	if len(req.Tools) > 0 {
		params.Tools = toolsToSDK(req.Tools)
	}
	if req.Temperature > 0 {
		params.Temperature = sdk.Float(req.Temperature)
	}
	st := c.sdk.Messages.NewStreaming(ctx, params)
	return &streamReader{stream: st, blockKind: map[int64]string{}}, nil
}
