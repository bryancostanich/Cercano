package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

// startupFallback lives for one dispatch, not on the shared provider. Once
// switched, subsequent model calls stay on cloud using the existing tool-loop
// history. It never owns or restarts the agentic runner. Calls within a dispatch
// are sequential, just like the tool loop that owns this wrapper.
type startupFallback struct {
	local, cloud inference.Provider
	model        string
	tier         config.Tier
	active       bool
	notice       string
	emit         func(agenttools.ProgressEvent)
}

func (p *startupFallback) Name() string {
	if p.active {
		return p.cloud.Name()
	}
	return p.local.Name()
}
func (p *startupFallback) Capabilities() inference.Capabilities {
	if p.active {
		return p.cloud.Capabilities()
	}
	return p.local.Capabilities()
}

func (e *Engine) startupFallback(sel inference.Selection, candidates inference.Tiers, mode locus.Mode, spec Spec, tier config.Tier) inference.Selection {
	if spec.RoutingTask != "" {
		return sel
	}
	policy := mode.Main()
	if spec.Role == RoleCoproc {
		policy = mode.Coproc()
	}
	if sel.IsCloud || !policy.CrossAllowed || policy.Preferred != locus.TierLocal || policy.Fallback != locus.TierCloud || candidates.Cloud == nil || candidates.Cloud.Name() == "NONE" || e.modelFor == nil {
		return sel
	}
	model := e.modelFor(true, tier)
	if model == "" {
		return sel
	}
	sel.Provider = &startupFallback{local: sel.Provider, cloud: candidates.Cloud, model: model, tier: tier, emit: spec.Emit}
	return sel
}

func (p *startupFallback) canFallback(ctx context.Context, err error) bool {
	var startup *llm.LocalStartupError
	return !p.active && ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && errors.As(err, &startup)
}

// cloudRequest validates the full request without deleting history or tool
// results. Unknown cloud windows fail closed instead of gambling with a request
// assembled for a different model. Provider-native token counts remain estimates.
func (p *startupFallback) cloudRequest(req llm.ChatRequest) (llm.ChatRequest, error) {
	req.Model = p.model
	req.Tier = string(p.tier)
	if req.MaxTokens <= 0 {
		req.MaxTokens = engine.DefaultMaxTokens
	}
	window := contextmeter.ModelWindowFor(req.Model)
	if !window.Known || window.Tokens <= 0 {
		return req, fmt.Errorf("local startup fallback: unknown context window for cloud model %q", req.Model)
	}
	if len(req.Tools) > 0 && !p.cloud.Capabilities().SupportsTools {
		return req, errors.New("local startup fallback: cloud provider does not support tools")
	}
	tok := contextmeter.Default()
	toolTokens := 0
	if len(req.Tools) > 0 {
		schemas, err := json.Marshal(req.Tools)
		if err != nil {
			return req, fmt.Errorf("local startup fallback: invalid tool schemas: %w", err)
		}
		toolTokens = tok.Count(string(schemas))
	}
	used := tok.Count(req.System) + compaction.ProviderTotalTokens(tok, req.Messages) + toolTokens + req.MaxTokens
	limit := int(float64(window.Tokens) * 0.85)
	if used > limit {
		return req, &llm.Error{Class: llm.ErrContextOverflow, Provider: p.cloud.Name(), Used: used, Limit: limit, Err: errors.New("local startup fallback request does not fit cloud context; history was not trimmed")}
	}
	return req, nil
}

func (p *startupFallback) switchToCloud() {
	if p.active {
		return
	}
	p.active = true
	p.notice = fmt.Sprintf("%s startup failed — switching this dispatch to %s (%s)", p.local.Name(), p.cloud.Name(), p.model)
	if p.emit != nil {
		p.emit(agenttools.ProgressEvent{Kind: "progress", Text: p.notice})
	}
}

func (p *startupFallback) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if !p.active {
		resp, err := p.local.Chat(ctx, req)
		if !p.canFallback(ctx, err) {
			return resp, err
		}
	}
	next, err := p.cloudRequest(req)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	p.switchToCloud()
	return p.cloud.Chat(ctx, next)
}

func (p *startupFallback) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	if !p.active {
		stream, err := p.local.StreamChat(ctx, req)
		// An opened stream is never retried, even if Next later returns a typed
		// error: output or tool calls might already have reached the consumer.
		if stream != nil || !p.canFallback(ctx, err) {
			return stream, err
		}
	}
	next, err := p.cloudRequest(req)
	if err != nil {
		return nil, err
	}
	p.switchToCloud()
	return p.cloud.StreamChat(ctx, next)
}

// CurrentRoute reports the route of the latest provider attempt. Consumers use
// it for result/error attribution; token totals still span the whole tool loop.
func CurrentRoute(sel inference.Selection, model string) (inference.Selection, string) {
	if p, ok := sel.Provider.(*startupFallback); ok && p.active {
		sel.IsCloud = true
		sel.FellBack = true
		sel.Notice = p.notice
		model = p.model
	}
	return sel, model
}

func (p *startupFallback) RuntimeContext(ctx context.Context, model string, prepare bool) (llm.RuntimeContext, error) {
	capacity, err := llm.ResolveRuntimeContext(ctx, p.local, model, prepare)
	if err == nil || !prepare || !p.canFallback(ctx, err) {
		return capacity, err
	}
	window := contextmeter.ModelWindowFor(p.model)
	if !window.Known || window.Tokens <= 0 {
		return llm.RuntimeContext{}, err
	}
	p.switchToCloud()
	return llm.RuntimeContext{Window: window.Tokens}, nil
}
