package anthropic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	// Route names the access path. "" / "direct" → vanilla Anthropic API.
	// "meridian" → emit opencode-style identification headers so the local
	// Meridian OAuth bridge routes through its OpenCode adapter (4-turn cap
	// instead of the default 3). See routeMeridian for the header set.
	Route string
}

// Known route values. Kept here so the adapter is the single place that
// names access paths it handles.
const (
	routeDirect   = "direct"
	routeMeridian = "meridian"
)

type ChatRequest = llm.ChatRequest
type ChatResponse = llm.ChatResponse

type Client struct {
	cfg Config
	sdk *sdk.Client
}

type sessionContextKey struct{}

// WithSessionID attaches a conversation/session ID to ctx so the adapter's
// RoundTripper can emit opencode-style identification headers per request.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, id)
}

func sessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sessionContextKey{}).(string); ok {
		return v
	}
	return ""
}

type headerRoundTripper struct {
	base  http.RoundTripper
	ua    string
	route string // when "meridian", emit opencode-style identification headers
}

func (h *headerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.ua != "" {
		r.Header.Set("User-Agent", h.ua)
	}
	// TODO(cercano-native-bridge-adapter): the opencode-* header set is a
	// borrowed identity — Cercano claims to be OpenCode so Meridian routes
	// through its OpenCode adapter (4-turn SDK cap vs the default 3-turn
	// cap which would break our 10-round tool loop). When Meridian (or any
	// successor bridge) ships a native Cercano adapter, swap these for
	// x-cercano-* and drop the dishonesty. See docs/agent/README.md.
	if h.route == routeMeridian {
		if sid := sessionIDFromContext(r.Context()); sid != "" {
			r.Header.Set("x-opencode-session", sid)
			r.Header.Set("x-opencode-request", newMessageID())
			r.Header.Set("x-opencode-agent-mode", "primary")
		}
	}
	return h.base.RoundTrip(r)
}

func newMessageID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "msg-" + hex.EncodeToString(b[:])
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
	opts = append(opts, option.WithHTTPClient(&http.Client{
		Transport: &headerRoundTripper{base: http.DefaultTransport, ua: cfg.UserAgent, route: cfg.Route},
	}))
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
