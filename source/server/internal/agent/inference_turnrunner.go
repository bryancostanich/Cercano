package agent

import (
	"context"
	"strings"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// inferenceTurnRunner is THE bridge between the two named layers: it adapts an
// inference.Provider (run one inference call, in blocks) up to a TurnRunner (run
// one routed turn, in text + routing metadata). Process runs a one-shot Chat
// (no tools), flattens text blocks into Response.Output, honors ModelOverride,
// and attributes the model that actually served (important on a failed-over
// call). This adapter is the ONLY place inference vocabulary is turned into
// turn vocabulary — the router layer never touches inference.Provider directly.
type inferenceTurnRunner struct {
	p     inference.Provider
	model string
}

// InferenceTurnRunner wraps an inference.Provider as a TurnRunner.
func InferenceTurnRunner(p inference.Provider, model string) TurnRunner {
	return &inferenceTurnRunner{p: p, model: model}
}

func (a *inferenceTurnRunner) Name() string { return a.p.Name() }

// processMaxTokens is the output budget for one-shot Process calls (same
// default as the tool loop). agent.Request carries no MaxTokens, and an unset
// budget must never reach the wire: api.anthropic.com answers max_tokens:0
// with a zero-token completion and no error — silent empty output.
const processMaxTokens = 4096

func (a *inferenceTurnRunner) Process(ctx context.Context, req *Request) (*Response, error) {
	model := a.model
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := processMaxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}
	chatResp, err := a.p.Chat(ctx, llm.ChatRequest{
		Model:          model,
		MaxTokens:      maxTokens,
		Temperature:    req.Temperature,
		Tier:           req.Tier,
		ConversationID: req.ConversationID,
		RequestID:      req.RequestID,
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
	// Attribute the model that actually served the call when the provider
	// reports one — on a failed-over call that's the backup's model, and
	// records must not claim the requested model served it.
	served := chatResp.Model
	if served == "" {
		served = model
	}
	return &Response{
		Output:          sb.String(),
		InputTokens:     chatResp.InputTokens,
		OutputTokens:    chatResp.OutputTokens,
		RoutingMetadata: RoutingMetadata{ModelName: served},
	}, nil
}
