package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	api "github.com/ollama/ollama/api"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

type Config struct {
	BaseURL string
	Model   string
}

type Client struct {
	cfg Config
	api *api.Client
}

type ChatRequest = llm.ChatRequest
type ChatResponse = llm.ChatResponse

func NewClient(cfg Config) *Client {
	u, _ := url.Parse(cfg.BaseURL)
	cli := api.NewClient(u, http.DefaultClient)
	return &Client{cfg: cfg, api: cli}
}

func (c *Client) Name() string { return "ollama" }

func (c *Client) Capabilities() inference.Capabilities {
	return inference.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: false,
		SupportsCaching:       false,
		SupportsVision:        true,
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	msgs := make([]api.Message, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, api.Message{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		om, err := messageToOllama(ctx, m)
		if err != nil {
			return ChatResponse{}, err
		}
		msgs = append(msgs, om)
	}
	opts := map[string]any{}
	if req.MaxTokens > 0 {
		opts["num_predict"] = req.MaxTokens
	}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	freq := &api.ChatRequest{
		Model:    req.Model,
		Messages: msgs,
		Stream:   boolPtr(false),
		Tools:    toolsToOllama(req.Tools),
		Options:  opts,
	}
	var got *api.ChatResponse
	err := c.api.Chat(ctx, freq, func(r api.ChatResponse) error {
		got = &r
		return nil
	})
	if err != nil {
		return ChatResponse{}, err
	}
	out := ChatResponse{
		StopReason:   "end_turn",
		InputTokens:  got.PromptEvalCount,
		OutputTokens: got.EvalCount,
		Model:        got.Model,
	}
	if got.Message.Content != "" {
		out.Blocks = append(out.Blocks, llm.Block{Type: llm.BlockText, Text: got.Message.Content})
	}
	for _, tc := range got.Message.ToolCalls {
		raw, _ := json.Marshal(tc.Function.Arguments)
		out.Blocks = append(out.Blocks, llm.Block{
			Type:      llm.BlockToolUse,
			ToolName:  tc.Function.Name,
			ToolInput: raw,
		})
	}
	return out, nil
}

func boolPtr(b bool) *bool { return &b }
