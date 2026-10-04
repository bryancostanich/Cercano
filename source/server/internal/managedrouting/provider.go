// Package managedrouting applies the pinned administrator routing plan using
// Cercano's existing adapters and retry/failover engine. It owns no credentials
// and never substitutes a personal backup for a missing managed route.
package managedrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/managedsettings"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

// Builder binds an approved physical identity to an existing local provider or
// credential profile. It must return a single adapter, without personal backups.
type Builder func(context.Context, v1.Route) (inference.Candidate, error)

// Build preserves the administrator's order. Construction failures remain nodes
// in the chain, so credential and permission errors cannot disappear into a
// fallback during assembly. Existing resilience owns retries and streaming rules.
func Build(ctx context.Context, plan managedsettings.RoutePlan, build Builder) (inference.Provider, error) {
	if len(plan.Routes) == 0 || build == nil {
		return nil, errors.New("managed route builder is unavailable")
	}
	nodes := make([]*boundRoute, len(plan.Routes))
	for i, route := range plan.Routes {
		candidate, err := build(ctx, route)
		if candidate.Provider == nil && err == nil {
			err = fmt.Errorf("managed route %q is unavailable", route.ID)
		}
		nodes[i] = &boundRoute{route: route, destination: plan.Destination, candidate: candidate, err: err, fallback: i > 0}
	}
	var next *chain
	for i := len(nodes) - 1; i >= 0; i-- {
		node := nodes[i]
		options := resilience.Options{PrimaryLabel: node.route.ID}
		if next != nil {
			options.Backup = next
			options.BackupLabel = next.first.route.ID
			model := next.first.route.Model
			options.BackupModelFor = func(string) string { return model }
		}
		next = &chain{Provider: resilience.New(node, options), first: node, next: next}
	}
	return next, nil
}

type chain struct {
	inference.Provider
	first            *boundRoute
	next             *chain
	preparedFallback atomic.Bool
}

func (p *chain) active() inference.Provider {
	if p.preparedFallback.Load() {
		return p.next
	}
	return p.Provider
}
func (p *chain) Name() string                         { return p.active().Name() }
func (p *chain) Capabilities() inference.Capabilities { return p.active().Capabilities() }
func (p *chain) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	return p.active().Chat(ctx, req)
}
func (p *chain) StreamChat(ctx context.Context, req inference.Call) (inference.Stream, error) {
	return p.active().StreamChat(ctx, req)
}
func (p *chain) TargetFor(model, tier string) llm.ServingRoute {
	return p.TargetForCall(inference.Call{Model: model, Tier: tier})
}
func (p *chain) TargetForCall(req inference.Call) llm.ServingRoute {
	return inference.TargetForCall(p.active(), req)
}
func (p *chain) TargetForContext(ctx context.Context, req inference.Call) llm.ServingRoute {
	return inference.TargetForContext(ctx, p.active(), req)
}
func (p *chain) RuntimeContext(ctx context.Context, _ string, prepare bool) (llm.RuntimeContext, error) {
	if p.preparedFallback.Load() {
		return p.next.RuntimeContext(ctx, "", prepare)
	}
	capacity, err := p.first.RuntimeContext(ctx, "", prepare)
	if err == nil || !prepare || p.next == nil || ctx.Err() != nil || !llm.Failoverable(llm.ClassOf(err), err) {
		return capacity, err
	}
	capacity, err = p.next.RuntimeContext(ctx, "", prepare)
	if err == nil {
		p.preparedFallback.Store(true)
	}
	return capacity, err
}

type boundRoute struct {
	route       v1.Route
	destination string
	candidate   inference.Candidate
	err         error
	fallback    bool
}

func (p *boundRoute) Name() string {
	if p.candidate.Provider != nil {
		return p.candidate.Provider.Name()
	}
	return p.route.Provider
}
func (p *boundRoute) Capabilities() inference.Capabilities {
	if p.candidate.Provider != nil {
		return p.candidate.Provider.Capabilities()
	}
	return inference.Capabilities{SupportsTools: true}
}
func (p *boundRoute) TargetForCall(_ inference.Call) llm.ServingRoute {
	route := inference.TargetFor(p.candidate.Provider, p.route.Model, "")
	route.Provider = p.Name()
	route.Model = p.route.Model
	route.Profile = p.candidate.Profile
	route.Destination = p.destination
	if p.route.Placement == "local" {
		route.Destination = "local"
	}
	return route
}
func (p *boundRoute) RuntimeContext(ctx context.Context, _ string, prepare bool) (llm.RuntimeContext, error) {
	if p.err != nil {
		return llm.RuntimeContext{}, p.err
	}
	return llm.ResolveRuntimeContext(ctx, p.candidate.Provider, p.route.Model, prepare)
}
func (p *boundRoute) request(ctx context.Context, req inference.Call) (context.Context, inference.Call, error) {
	if p.err != nil {
		return ctx, req, p.err
	}
	req.Model = p.route.Model
	// Model identities are explicit in a managed plan. A saved quality-tier model
	// or a previous provider's model must not overwrite the selected identity.
	req.Tier = ""
	req.FallbackTier = ""
	if p.fallback {
		ctx = llm.WithRuntimeContext(ctx, llm.RuntimeContext{})
	}
	capacity, err := p.RuntimeContext(ctx, p.route.Model, true)
	if err != nil {
		return ctx, req, err
	}
	if capacity.InstanceID != "" {
		if err := llm.CheckRuntimeContext(ctx, capacity); err != nil {
			return ctx, req, err
		}
		ctx = llm.WithRuntimeContext(ctx, capacity)
	}
	if p.fallback {
		if err := p.checkFallbackRequest(req, capacity); err != nil {
			return ctx, req, err
		}
	}
	return ctx, req, nil
}
func (p *boundRoute) checkFallbackRequest(req inference.Call, capacity llm.RuntimeContext) error {
	if len(req.Tools) > 0 && !p.Capabilities().SupportsTools {
		return &llm.Error{Class: llm.ErrInvalidRequest, Err: errors.New("approved fallback does not support tools")}
	}
	window := capacity.Window
	if window == 0 {
		target := p.TargetForCall(req)
		if target.ContextWindowKnown {
			window = target.ContextWindow
		} else if known := contextmeter.ModelWindowFor(p.route.Model); known.Known {
			window = known.Tokens
		}
	}
	if window <= 0 {
		return &llm.Error{Class: llm.ErrInvalidRequest, Err: fmt.Errorf("approved fallback %q has no known context capacity", p.route.ID)}
	}
	tok := contextmeter.Default()
	toolTokens := 0
	if len(req.Tools) > 0 {
		raw, err := json.Marshal(req.Tools)
		if err != nil {
			return err
		}
		toolTokens = tok.Count(string(raw))
	}
	output := req.MaxTokens
	if output <= 0 {
		output = engine.DefaultMaxTokens
	}
	used := tok.Count(req.System) + compaction.ProviderTotalTokens(tok, req.Messages) + toolTokens + output
	limit := int(float64(window) * 0.85)
	if used > limit {
		return &llm.Error{Class: llm.ErrContextOverflow, Used: used, Limit: limit, Err: errors.New("request does not fit the approved fallback; history was not truncated")}
	}
	return nil
}
func (p *boundRoute) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	ctx, req, err := p.request(ctx, req)
	if err != nil {
		return inference.Result{}, err
	}
	result, err := p.candidate.Provider.Chat(ctx, req)
	if err == nil {
		route := p.TargetForCall(req)
		result.Route = &route
		if result.Model == "" {
			result.Model = p.route.Model
		}
	}
	return result, err
}
func (p *boundRoute) StreamChat(ctx context.Context, req inference.Call) (inference.Stream, error) {
	ctx, req, err := p.request(ctx, req)
	if err != nil {
		return nil, err
	}
	stream, err := p.candidate.Provider.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &routeStream{StreamReader: stream, route: p.TargetForCall(req)}, nil
}

type routeStream struct {
	llm.StreamReader
	route llm.ServingRoute
}

func (s *routeStream) Next() (llm.StreamEvent, bool, error) {
	event, ok, err := s.StreamReader.Next()
	if ok && event.Type == llm.EventMessageStart {
		route := s.route
		event.Route = &route
	}
	return event, ok, err
}
