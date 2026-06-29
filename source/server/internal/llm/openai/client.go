package openai

import (
	"context"
	"encoding/base64"
	"net/http"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

// Config holds the OpenAI client configuration.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Backend string // selects per-backend quirks; empty → defensive default
}

// Client implements llm.Provider using the OpenAI chat completions API.
type Client struct {
	api    *goopenai.Client
	model  string
	quirks Quirks
}

// NewClient constructs a Client from cfg. The HTTP transport is wrapped in a
// normalizingDoer so per-backend response quirks (transient retries, array-shaped
// error bodies) are repaired before go-openai parses them.
func NewClient(cfg Config) *Client {
	c := goopenai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		c.BaseURL = cfg.BaseURL
	}
	q := quirksFor(cfg.Backend)
	c.HTTPClient = &normalizingDoer{next: &http.Client{}, quirks: q}
	return &Client{api: goopenai.NewClientWithConfig(c), model: cfg.Model, quirks: q}
}

// resolveImageURLs replaces URL image blocks with inline base64, so backends
// that won't fetch image URLs (e.g. the Gemini-compat shim) still receive the
// image. Called only when quirks.ImagesAsBase64 is set. Text blocks and
// already-base64 image blocks pass through. The caller's slice is not mutated
// (copy-on-write per message).
func resolveImageURLs(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		blocks := m.Blocks
		copied := false
		for j, b := range m.Blocks {
			if b.Type != llm.BlockImage || b.ImageURL == "" {
				continue
			}
			data, err := llm.ResolveImageBytes(ctx, b)
			if err != nil {
				return nil, err
			}
			if !copied {
				blocks = append([]llm.Block(nil), m.Blocks...)
				copied = true
			}
			blocks[j].ImageURL = ""
			blocks[j].ImageData = base64.StdEncoding.EncodeToString(data)
			blocks[j].MediaType = http.DetectContentType(data)
		}
		out[i].Blocks = blocks
	}
	return out, nil
}

func (c *Client) Name() string { return "openai" }

func (c *Client) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: true,
		SupportsCaching:       false,
		SupportsVision:        true,
	}
}

func (c *Client) buildRequest(req llm.ChatRequest, stream bool) goopenai.ChatCompletionRequest {
	r := goopenai.ChatCompletionRequest{
		Model:    modelOr(c.model, req.Model),
		Messages: messagesToOpenAI(req.Messages, req.System),
		Tools:    toolsToOpenAI(req.Tools),
		Stream:   stream,
	}
	if stream {
		// Request usage on the final chunk so InputTokens/OutputTokens are
		// available for EventMessageStop.
		r.StreamOptions = &goopenai.StreamOptions{IncludeUsage: true}
	}
	if req.MaxTokens > 0 {
		r.MaxTokens = req.MaxTokens
	}
	return r
}

func modelOr(def, override string) string {
	if override != "" {
		return override
	}
	return def
}

// Chat sends a non-streaming chat completion request and returns mapped blocks + usage.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if c.quirks.ImagesAsBase64 {
		msgs, err := resolveImageURLs(ctx, req.Messages)
		if err != nil {
			return llm.ChatResponse{}, err
		}
		req.Messages = msgs
	}
	resp, err := c.api.CreateChatCompletion(ctx, c.buildRequest(req, false))
	if err != nil {
		return llm.ChatResponse{}, err
	}
	out := llm.ChatResponse{
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}
	if len(resp.Choices) > 0 {
		out.Blocks = blocksFromOpenAI(resp.Choices[0].Message)
		out.StopReason = string(resp.Choices[0].FinishReason)
	}
	return out, nil
}

// StreamChat opens a streaming chat completion and returns a StreamReader that
// emits llm.StreamEvents following the START→DELTA→STOP contract.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	if c.quirks.ImagesAsBase64 {
		msgs, err := resolveImageURLs(ctx, req.Messages)
		if err != nil {
			return nil, err
		}
		req.Messages = msgs
	}
	stream, err := c.api.CreateChatCompletionStream(ctx, c.buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	return newStreamReader(stream), nil
}
