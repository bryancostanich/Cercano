package llamaserver

import (
	"context"
	"strings"

	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/openai"
)

// LLMProvider adapts the llama-server runtime to the native inference.Provider
// interface. Each call resolves (and warms, when needed) the runtime instance
// serving the requested model via endpointFor, then delegates the chat to an
// OpenAI-compatible client pointed at that instance — llama-server exposes
// /v1/chat/completions natively. This is what lets the dispatch engine's open
// lane (watchdog checks, coproc capabilities, sub-agent loops under a local
// locus) follow the configured runtime instead of silently requiring an
// Ollama daemon.
type LLMProvider struct {
	eng *Engine
}

// NewLLMProvider wraps eng as a native inference.Provider.
func NewLLMProvider(eng *Engine) *LLMProvider { return &LLMProvider{eng: eng} }

func (p *LLMProvider) Name() string { return "llama_server" }

func (p *LLMProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
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
// bound to it, plus the request rewritten to the resolved model id (llama-server
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
	if req.MaxTokens <= 0 {
		req.MaxTokens = engine.DefaultMaxTokens
	}
	// SupportsVision reflects the resolved model's real capability: true only
	// for a vision model launched with its mmproj. Phase 1 lands the capability
	// gate with this defaulted false (so images are stripped rather than sent to
	// a backend that would 500); Phase 2 threads the actual per-model flag
	// through endpointFor from the resolved ModelRecord.
	vision := false
	c := openai.NewClient(openai.Config{
		BaseURL:        strings.TrimRight(endpoint, "/") + "/v1",
		Model:          model,
		Backend:        "llama_server",
		SupportsVision: vision,
	})
	return c, req, nil
}
