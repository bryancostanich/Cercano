package managedrouting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/modelpolicy"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

type routeFixture struct {
	route      v1.Route
	calls      *[]string
	err        error
	events     []llm.StreamEvent
	streamErr  error
	prepareErr error
	window     int
	after      func()
}

func (p *routeFixture) Name() string { return p.route.Provider }
func (p *routeFixture) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *routeFixture) TargetFor(string, string) llm.ServingRoute {
	return llm.ServingRoute{ContextWindow: p.window, ContextWindowKnown: p.window > 0}
}
func (p *routeFixture) RuntimeContext(context.Context, string, bool) (llm.RuntimeContext, error) {
	return llm.RuntimeContext{Window: p.window, InstanceID: "runtime"}, p.prepareErr
}
func (p *routeFixture) record(ctx context.Context, r inference.Call) error {
	if r.Model != p.route.Model || r.Tier != "" || r.FallbackTier != "" {
		return errors.New("personal model or tier escaped managed binding")
	}
	if err := modelpolicy.Check(ctx, modelpolicy.Attempt{Provider: p.route.Provider, Endpoint: p.route.Endpoint, Model: r.Model, Placement: p.route.Placement}); err != nil {
		return err
	}
	*p.calls = append(*p.calls, p.route.ID)
	if p.after != nil {
		p.after()
	}
	return p.err
}
func (p *routeFixture) Chat(ctx context.Context, r inference.Call) (inference.Result, error) {
	if err := p.record(ctx, r); err != nil {
		return inference.Result{}, err
	}
	return inference.Result{Model: r.Model}, nil
}
func (p *routeFixture) StreamChat(ctx context.Context, r inference.Call) (inference.Stream, error) {
	if err := p.record(ctx, r); err != nil {
		return nil, err
	}
	events := p.events
	if events == nil {
		events = []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventTextDelta, TextDelta: p.route.ID}, {Type: llm.EventMessageStop}}
	}
	return &fixtureStream{events: events, err: p.streamErr}, nil
}

type fixtureStream struct {
	events []llm.StreamEvent
	err    error
}

func (s *fixtureStream) Next() (llm.StreamEvent, bool, error) {
	if len(s.events) == 0 {
		return llm.StreamEvent{}, false, s.err
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, true, nil
}
func (*fixtureStream) Close() error { return nil }
func testPlan() managedsettings.RoutePlan {
	return managedsettings.RoutePlan{Task: "chat", Destination: "primary", Quality: "standard", Routes: []v1.Route{
		{ID: "first", Provider: "openai", Endpoint: "https://a.example/v1", Model: "model-a", Placement: "external"},
		{ID: "second", Provider: "openai", Endpoint: "https://b.example/v1", Model: "model-b", Placement: "external"},
		{ID: "third", Provider: "anthropic", Endpoint: "https://c.example", Model: "model-c", Placement: "external"},
	}}
}
func networkError() error {
	return &llm.Error{Class: llm.ErrNetwork, Err: errors.New("fixture offline")}
}
func TestManagedChainUsesExplicitOrderAndFixedModels(t *testing.T) {
	plan := testPlan()
	var calls []string
	provider, err := Build(context.Background(), plan, func(_ context.Context, r v1.Route) (inference.Candidate, error) {
		p := &routeFixture{route: r, calls: &calls, window: 32768}
		if r.ID != "third" {
			p.err = networkError()
		}
		return inference.Candidate{Provider: p, Profile: r.ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Chat(context.Background(), inference.Call{Model: "personal-model", Tier: "most_capable", FallbackTier: "everyday"})
	if err != nil || strings.Join(calls, ",") != "first,first,second,second,third" || result.Model != "model-c" || result.Route.Profile != "third" {
		t.Fatalf("calls=%v result=%+v err=%v", calls, result, err)
	}
}
func TestRevokedFallbackStopsWithoutTryingAnotherApprovedRoute(t *testing.T) {
	plan := testPlan()
	var calls []string
	revoked := false
	ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
		if revoked {
			return modelpolicy.Deny(a, "membership revoked")
		}
		return nil
	}))
	provider, err := Build(ctx, plan, func(_ context.Context, r v1.Route) (inference.Candidate, error) {
		p := &routeFixture{route: r, calls: &calls, window: 32768}
		if r.ID == "first" {
			p.err = &llm.Error{Class: llm.ErrQuota, Err: errors.New("fixture quota")}
			p.after = func() { revoked = true }
		}
		return inference.Candidate{Provider: p}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(ctx, inference.Call{})
	if !modelpolicy.IsDenial(err) || strings.Join(calls, ",") != "first" {
		t.Fatalf("revoked fallback sent work: calls=%v err=%v", calls, err)
	}
}
func TestManagedStreamFallsBackOnlyBeforeContent(t *testing.T) {
	for _, partial := range []bool{false, true} {
		plan := testPlan()
		var calls []string
		provider, err := Build(context.Background(), plan, func(_ context.Context, r v1.Route) (inference.Candidate, error) {
			p := &routeFixture{route: r, calls: &calls, window: 32768}
			if r.ID == "first" {
				p.events = []llm.StreamEvent{}
				if partial {
					p.events = append(p.events, llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: "partial"})
				}
				p.streamErr = networkError()
			}
			return inference.Candidate{Provider: p}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		stream, err := provider.StreamChat(context.Background(), inference.Call{})
		if err != nil {
			t.Fatal(err)
		}
		var text string
		for {
			event, ok, nextErr := stream.Next()
			if nextErr != nil {
				err = nextErr
				break
			}
			if !ok {
				break
			}
			if event.Type == llm.EventTextDelta {
				text += event.TextDelta
			}
		}
		stream.Close()
		if partial {
			if err == nil || strings.Join(calls, ",") != "first" || text != "partial" {
				t.Fatalf("replayed partial output: calls=%v text=%q err=%v", calls, text, err)
			}
		} else if err != nil || strings.Join(calls, ",") != "first,first,second" || text != "second" {
			t.Fatalf("no permitted stream fallback: calls=%v text=%q err=%v", calls, text, err)
		}
	}
}
func TestManagedFallbackChecksCapacityWithoutTruncatingRequest(t *testing.T) {
	plan := testPlan()
	plan.Routes = plan.Routes[:2]
	var calls []string
	provider, err := Build(context.Background(), plan, func(_ context.Context, r v1.Route) (inference.Candidate, error) {
		p := &routeFixture{route: r, calls: &calls, window: 100}
		if r.ID == "first" {
			p.err = networkError()
		}
		return inference.Candidate{Provider: p}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), inference.Call{System: "Keep all original instructions.", MaxTokens: 200})
	if llm.ClassOf(err) != llm.ErrContextOverflow || strings.Join(calls, ",") != "first,first" {
		t.Fatalf("oversized fallback request sent: calls=%v err=%v", calls, err)
	}
}
func TestManagedLocalPreparationUsesOnlyExplicitFallback(t *testing.T) {
	plan := testPlan()
	plan.Routes = plan.Routes[:2]
	plan.Routes[0].Provider = "llama_server"
	plan.Routes[0].Placement = "local"
	plan.Routes[0].Endpoint = "http://127.0.0.1:8080/v1"
	var calls []string
	provider, err := Build(context.Background(), plan, func(_ context.Context, r v1.Route) (inference.Candidate, error) {
		p := &routeFixture{route: r, calls: &calls, window: 32768}
		if r.ID == "first" {
			p.prepareErr = networkError()
		}
		return inference.Candidate{Provider: p}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = llm.ResolveRuntimeContext(context.Background(), provider, plan.Routes[0].Model, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Chat(context.Background(), inference.Call{})
	if err != nil || result.Model != "model-b" || strings.Join(calls, ",") != "second" {
		t.Fatalf("local startup fallback: calls=%v result=%+v err=%v", calls, result, err)
	}
}
