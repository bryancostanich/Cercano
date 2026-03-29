package llm

import (
	"context"
	"sync"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/engine"
)

type LocalModelProvider struct {
	mu        sync.RWMutex
	ModelName string
	Engine    engine.InferenceEngine
}

func NewLocalModelProvider(engine engine.InferenceEngine, modelName string) *LocalModelProvider {
	return &LocalModelProvider{
		ModelName: modelName,
		Engine:    engine,
	}
}

// SetModelName updates the model name at runtime (thread-safe).
func (p *LocalModelProvider) SetModelName(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ModelName = name
}

func (p *LocalModelProvider) Name() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModelName
}

func (p *LocalModelProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	p.mu.RLock()
	modelName := p.ModelName
	eng := p.Engine
	p.mu.RUnlock()

	// Per-request model override (e.g., research uses a different model)
	if req.ModelOverride != "" {
		modelName = req.ModelOverride
	}

	result, err := eng.Complete(ctx, modelName, req.Input, "")
	if err != nil {
		return nil, err
	}

	return &agent.Response{
		Output:       result.Output,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
	}, nil
}

func (p *LocalModelProvider) ProcessStream(ctx context.Context, req *agent.Request, onToken agent.TokenFunc) (*agent.Response, error) {
	p.mu.RLock()
	modelName := p.ModelName
	eng := p.Engine
	p.mu.RUnlock()

	result, err := eng.CompleteStream(ctx, modelName, req.Input, "", func(t string) {
		if onToken != nil {
			onToken(t)
		}
	})
	if err != nil {
		return nil, err
	}

	return &agent.Response{
		Output:       result.Output,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
	}, nil
}
