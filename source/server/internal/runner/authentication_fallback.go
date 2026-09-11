package runner

import (
	"context"
	"errors"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// authenticationFallback belongs to one turn. It consumes only an explicit
// authentication fallback choice and switches at the inference-call boundary,
// never by rerunning the tool loop. Subsequent calls in that authorized turn
// use the selected destination; the next user turn gets a new wrapper.
type authenticationFallback struct {
	primary, fallback inference.Provider
	model             string
	window            int
	selected          bool
	onSelect          func()
}

func (p *authenticationFallback) Name() string {
	if p.selected {
		return p.fallback.Name()
	}
	return p.primary.Name()
}
func (p *authenticationFallback) Capabilities() inference.Capabilities {
	if p.selected {
		return p.fallback.Capabilities()
	}
	return p.primary.Capabilities()
}
func (p *authenticationFallback) RuntimeContext(ctx context.Context, model string, prepare bool) (llm.RuntimeContext, error) {
	if p.selected {
		return llm.ResolveRuntimeContext(ctx, p.fallback, p.model, prepare)
	}
	return llm.ResolveRuntimeContext(ctx, p.primary, model, prepare)
}
func (p *authenticationFallback) available(req inference.Call) bool {
	caps := p.fallback.Capabilities()
	if len(req.Tools) > 0 && !caps.SupportsTools {
		return false
	}
	if !caps.SupportsVision {
		for _, m := range req.Messages {
			for _, b := range m.Blocks {
				if b.Type == llm.BlockImage {
					return false
				}
			}
		}
	}
	if p.window > 0 {
		budget := agent.EstimateRequestBudget(agent.RequestBudgetInput{System: req.System, Messages: req.Messages, Tools: req.Tools, MaxTokens: req.MaxTokens, ContextWindow: p.window})
		if !budget.Fits {
			return false
		}
	}
	return true
}
func (p *authenticationFallback) callContext(ctx context.Context, req inference.Call) context.Context {
	name := ""
	if p.available(req) {
		name = p.model + " (" + p.fallback.Name() + ")"
	}
	return llm.WithExternalAuthFallback(ctx, name)
}
func (p *authenticationFallback) selectFallback(err error) bool {
	var choice *llm.AuthFallbackRequest
	if p.selected || !errors.As(err, &choice) {
		return false
	}
	p.selected = true
	if p.onSelect != nil {
		p.onSelect()
	}
	return true
}
func (p *authenticationFallback) prepare(ctx context.Context, req inference.Call) (context.Context, inference.Call, error) {
	req.Model = p.model
	if p.fallback.Name() == "llama_server" {
		runtime, err := llm.ResolveRuntimeContext(ctx, p.fallback, p.model, true)
		if err != nil {
			return ctx, req, err
		}
		ctx = llm.WithRuntimeContext(ctx, runtime)
		p.window = runtime.Window
	}
	if !p.available(req) {
		return ctx, req, errors.New("selected fallback cannot safely serve this request")
	}
	return llm.WithExternalAuthFallback(ctx, ""), req, nil
}
func terminalFallback(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	return &llm.CredentialError{Class: llm.ErrCredential, Reason: "selected fallback failed; request was not replayed", Cause: err}
}
func (p *authenticationFallback) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	if !p.selected {
		result, err := p.primary.Chat(p.callContext(ctx, req), req)
		if !p.selectFallback(err) {
			return result, err
		}
	}
	ctx, req, err := p.prepare(ctx, req)
	if err != nil {
		return inference.Result{}, terminalFallback(err)
	}
	result, err := p.fallback.Chat(ctx, req)
	return result, terminalFallback(err)
}
func (p *authenticationFallback) StreamChat(ctx context.Context, req inference.Call) (inference.Stream, error) {
	if p.selected {
		return p.fallbackStream(ctx, req)
	}
	stream, err := p.primary.StreamChat(p.callContext(ctx, req), req)
	if p.selectFallback(err) {
		return p.fallbackStream(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	return &authenticationFallbackStream{owner: p, ctx: ctx, req: req, inner: stream}, nil
}
func (p *authenticationFallback) fallbackStream(ctx context.Context, req inference.Call) (inference.Stream, error) {
	ctx, req, err := p.prepare(ctx, req)
	if err != nil {
		return nil, terminalFallback(err)
	}
	stream, err := p.fallback.StreamChat(ctx, req)
	if err != nil {
		return nil, terminalFallback(err)
	}
	return &authenticationFallbackStream{owner: p, ctx: ctx, req: req, inner: stream, selected: true}, nil
}

type authenticationFallbackStream struct {
	owner             *authenticationFallback
	ctx               context.Context
	req               inference.Call
	inner             inference.Stream
	emitted, selected bool
}

func (s *authenticationFallbackStream) Next() (llm.StreamEvent, bool, error) {
	event, ok, err := s.inner.Next()
	if err == nil && ok && event.Type == llm.EventError {
		err = event.Err
	}
	if !s.emitted && !s.selected && s.owner.selectFallback(err) {
		s.inner.Close()
		next, e := s.owner.fallbackStream(s.ctx, s.req)
		if e != nil {
			return llm.StreamEvent{}, false, e
		}
		s.inner = next
		s.selected = true
		return s.Next()
	}
	if s.selected && err != nil {
		return llm.StreamEvent{}, false, terminalFallback(err)
	}
	if ok && event.Type != llm.EventNotice {
		s.emitted = true
	}
	return event, ok, err
}
func (s *authenticationFallbackStream) Close() error { return s.inner.Close() }
