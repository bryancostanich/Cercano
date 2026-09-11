package profilechain

import (
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/modelmetadata"
	"context"
	"fmt"
)

type routeProvider struct {
	inference.Provider
	profile, destination string
}

func (p *routeProvider) TargetFor(model, tier string) llm.ServingRoute {
	route := llm.ServingRoute{Provider: p.Provider.Name(), Profile: p.profile, Destination: p.destination, Model: model, ContextWindow: contextmeter.ModelWindowFor("").Tokens}
	if source, ok := p.Provider.(interface {
		ModelEvidence(string) modelmetadata.Evidence
	}); ok {
		e := source.ModelEvidence(model)
		route.VisionKnown = e.Vision != modelmetadata.VisionUnknown
		route.SupportsVision = e.Vision == modelmetadata.VisionSupported
		if e.ContextWindow > 0 {
			route.ContextWindow = e.ContextWindow
			route.ContextWindowKnown = true
		}
	}
	return route
}
func (p *routeProvider) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	if req.Model == "" {
		return inference.Result{}, fmt.Errorf("profile %q has no model for requested intent", p.profile)
	}
	result, err := p.Provider.Chat(ctx, req)
	if err == nil {
		model := result.Model
		if model == "" {
			model = req.Model
			result.Model = model
		}
		route := p.TargetFor(model, req.Tier)
		result.Route = &route
	}
	return result, err
}
func (p *routeProvider) StreamChat(ctx context.Context, req inference.Call) (inference.Stream, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("profile %q has no model for requested intent", p.profile)
	}
	stream, err := p.Provider.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &routeStream{StreamReader: stream, route: p.TargetFor(req.Model, req.Tier)}, nil
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
