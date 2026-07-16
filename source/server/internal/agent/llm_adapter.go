package agent

import (
	"context"
	"strings"

	"cercano/source/server/internal/llm"
)

// llmModelProvider adapts an llm.Provider (native tool-calling interface) to the
// legacy agent.ModelProvider interface, so a single cloud profile can serve both
// the tool-loop and the co-processor CloudModel slot. Process runs a one-shot
// Chat (no tools) and concatenates text blocks into Response.Output.
type llmModelProvider struct {
	p     llm.Provider
	model string
}

// NewLLMModelProvider wraps an llm.Provider as a ModelProvider.
func NewLLMModelProvider(p llm.Provider, model string) ModelProvider {
	return &llmModelProvider{p: p, model: model}
}

func (a *llmModelProvider) Name() string { return a.p.Name() }

// processMaxTokens is the output budget for one-shot Process calls (same
// default as the tool loop). agent.Request carries no MaxTokens, and an unset
// budget must never reach the wire: api.anthropic.com answers max_tokens:0
// with a zero-token completion and no error — silent empty output.
const processMaxTokens = 4096

func (a *llmModelProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	model := a.model
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	chatResp, err := a.p.Chat(ctx, llm.ChatRequest{
		Model:     model,
		MaxTokens: processMaxTokens,
		Messages: []llm.Message{
			{
				Role:   llm.RoleUser,
				Blocks: buildUserBlocks(req.Input, req.Images),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	for _, b := range chatResp.Blocks {
		if b.Type == llm.BlockText {
			sb.WriteString(b.Text)
		}
	}
	return &Response{
		Output:          sb.String(),
		InputTokens:     chatResp.InputTokens,
		OutputTokens:    chatResp.OutputTokens,
		RoutingMetadata: RoutingMetadata{ModelName: model},
	}, nil
}
