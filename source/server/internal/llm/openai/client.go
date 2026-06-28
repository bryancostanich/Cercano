package openai

import (
	"context"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

// Config holds the OpenAI client configuration.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Client implements llm.Provider using the OpenAI chat completions API.
type Client struct {
	api   *goopenai.Client
	model string
}

// NewClient constructs a Client from cfg.
func NewClient(cfg Config) *Client {
	c := goopenai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		c.BaseURL = cfg.BaseURL
	}
	return &Client{api: goopenai.NewClientWithConfig(c), model: cfg.Model}
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
	stream, err := c.api.CreateChatCompletionStream(ctx, c.buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	return newStreamReader(stream), nil
}
