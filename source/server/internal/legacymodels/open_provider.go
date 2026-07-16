package legacymodels

import (
	"context"
	"sync"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/engine"
)

// OpenModelResolver maps a requested open-model name to the canonical name the
// engine should actually serve, going through the engine-agnostic model
// lifecycle (runtime manager's ResolveOpenModel). It returns an error when the
// model can't be resolved to a present on-disk model, so the provider degrades
// (surfaces the error) instead of handing the engine an unresolved name that
// fails at load — the compaction "not downloaded" bug.
//
// Injected from the server (which owns the runtime manager) to keep this
// package free of a localruntime import.
type OpenModelResolver func(ctx context.Context, requested string) (served string, err error)

type OpenModelProvider struct {
	mu        sync.RWMutex
	ModelName string
	Engine    engine.InferenceEngine
	// resolve, when set, canonicalizes the model name through the runtime
	// lifecycle before each Complete. Nil → legacy behavior (raw name passed
	// straight to the engine), preserving existing tests/wiring.
	resolve OpenModelResolver
}

func NewOpenModelProvider(engine engine.InferenceEngine, modelName string) *OpenModelProvider {
	return &OpenModelProvider{
		ModelName: modelName,
		Engine:    engine,
	}
}

// SetModelName updates the model name at runtime (thread-safe).
func (p *OpenModelProvider) SetModelName(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ModelName = name
}

// SetEngine updates the local inference backend at runtime.
func (p *OpenModelProvider) SetEngine(eng engine.InferenceEngine, modelName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Engine = eng
	if modelName != "" {
		p.ModelName = modelName
	}
}

// SetResolver installs (or clears, with nil) the lifecycle resolver that
// canonicalizes model names to present on-disk models before each Complete.
func (p *OpenModelProvider) SetResolver(r OpenModelResolver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resolve = r
}

func (p *OpenModelProvider) Name() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModelName
}

// resolveModel maps the effective model name through the lifecycle resolver
// when one is installed. Without a resolver it returns the name unchanged
// (legacy behavior). A resolver error propagates so the caller degrades rather
// than dispatching an unresolved name to the engine.
func (p *OpenModelProvider) resolveModel(ctx context.Context, modelName string) (string, error) {
	p.mu.RLock()
	resolve := p.resolve
	p.mu.RUnlock()
	if resolve == nil {
		return modelName, nil
	}
	return resolve(ctx, modelName)
}

func (p *OpenModelProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	p.mu.RLock()
	modelName := p.ModelName
	eng := p.Engine
	p.mu.RUnlock()

	// Per-request model override (e.g., research uses a different model)
	if req.ModelOverride != "" {
		modelName = req.ModelOverride
	}

	// Canonicalize through the lifecycle resolver: turn the configured/override
	// name into a present on-disk model, or fail here (degrade) rather than
	// hand the engine a name it can't load.
	modelName, err := p.resolveModel(ctx, modelName)
	if err != nil {
		return nil, err
	}

	result, err := eng.Complete(ctx, modelName, req.Input, "", engine.GenOptions{Temperature: req.Temperature})
	if err != nil {
		return nil, err
	}

	return &agent.Response{
		Output:       result.Output,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
	}, nil
}

func (p *OpenModelProvider) ProcessStream(ctx context.Context, req *agent.Request, onToken agent.TokenFunc) (*agent.Response, error) {
	p.mu.RLock()
	modelName := p.ModelName
	eng := p.Engine
	p.mu.RUnlock()

	if req.ModelOverride != "" {
		modelName = req.ModelOverride
	}

	modelName, err := p.resolveModel(ctx, modelName)
	if err != nil {
		return nil, err
	}

	result, err := eng.CompleteStream(ctx, modelName, req.Input, "", engine.GenOptions{Temperature: req.Temperature}, func(t string) {
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
