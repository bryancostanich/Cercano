package mistralrs

import (
	"context"
	"strings"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/openai"
)

// LLMProvider adapts the mistral.rs runtime to the native llm.Provider
// interface. Each call resolves (and warms, when needed) the runtime instance
// serving the requested model via endpointFor, then delegates the chat to an
// OpenAI-compatible client pointed at that instance — mistral.rs exposes
// /v1/chat/completions natively. This is what lets the dispatch engine's open
// lane (watchdog checks, coproc capabilities, sub-agent loops under a local
// locus) follow the configured runtime instead of silently requiring an
// Ollama daemon.
type LLMProvider struct {
	eng *Engine
}

// NewLLMProvider wraps eng as a native llm.Provider.
func NewLLMProvider(eng *Engine) *LLMProvider { return &LLMProvider{eng: eng} }

func (p *LLMProvider) Name() string { return "mistralrs" }

func (p *LLMProvider) Capabilities() llm.Capabilities {
	return llm.Capabilities{SupportsTools: true}
}

func (p *LLMProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	c, req, err := p.clientFor(ctx, req)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	return c.Chat(ctx, req)
}

func (p *LLMProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	c, req, err := p.clientFor(ctx, req)
	if err != nil {
		return nil, err
	}
	return c.StreamChat(ctx, req)
}

// clientFor resolves the instance endpoint for req.Model and returns a client
// bound to it, plus the request rewritten to the resolved model id (mistral.rs
// serves one model per instance; the id in the request is informational).
func (p *LLMProvider) clientFor(ctx context.Context, req llm.ChatRequest) (*openai.Client, llm.ChatRequest, error) {
	endpoint, model, err := p.eng.endpointFor(ctx, req.Model)
	if err != nil {
		return nil, req, err
	}
	if model == "" {
		model = "default"
	}
	req.Model = model
	c := openai.NewClient(openai.Config{
		BaseURL: strings.TrimRight(endpoint, "/") + "/v1",
		Model:   model,
	})
	return c, req, nil
}
