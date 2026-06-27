package agent

import (
	"context"
	"testing"
)

// fakeProvider is a minimal ModelProvider for routing tests.
// Satisfies ModelProvider: Process + Name (no ProcessStream needed).
type fakeProvider struct {
	name string
	out  string
}

func (f *fakeProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return &Response{Output: f.out, InputTokens: 1, OutputTokens: 1}, nil
}
func (f *fakeProvider) Name() string { return f.name }

// fakeCoprocRouter returns a fixed provider map.
// Satisfies Router: ClassifyIntent + SelectProvider + GetModelProviders.
type fakeCoprocRouter struct{ providers map[string]ModelProvider }

func (r *fakeCoprocRouter) ClassifyIntent(req *Request) (Intent, error) {
	return IntentChat, nil
}
func (r *fakeCoprocRouter) SelectProvider(req *Request, i Intent) (ModelProvider, error) {
	return r.providers["LocalModel"], nil
}
func (r *fakeCoprocRouter) GetModelProviders() map[string]ModelProvider { return r.providers }

func newCoprocAgent(mode, localName, cloudName string) *Agent {
	provs := map[string]ModelProvider{
		"LocalModel": &fakeProvider{name: localName, out: "local-out"},
		"CloudModel": &fakeProvider{name: cloudName, out: "cloud-out"},
	}
	a := NewAgent(&fakeCoprocRouter{providers: provs}, nil)
	a.SetLocusModeGetter(func() string { return mode })
	return a
}

func TestCoprocRoutesPerMode(t *testing.T) {
	ctx := context.Background()

	// local_primary → local
	if r, err := newCoprocAgent("local_primary", "ollama", "anthropic").
		ProcessRequest(ctx, &Request{Input: "x", Coproc: true}); err != nil || r.RoutingMetadata.ModelName != "ollama" || r.RoutingMetadata.IsCloud {
		t.Errorf("local_primary coproc: %+v err=%v", r.RoutingMetadata, err)
	}
	// cloud_only → cloud
	if r, err := newCoprocAgent("cloud_only", "ollama", "anthropic").
		ProcessRequest(ctx, &Request{Input: "x", Coproc: true}); err != nil || r.RoutingMetadata.ModelName != "anthropic" || !r.RoutingMetadata.IsCloud {
		t.Errorf("cloud_only coproc: %+v err=%v", r.RoutingMetadata, err)
	}
}

func TestCoprocCloudOnlyHardFailsWhenAbsent(t *testing.T) {
	// CloudModel is the absent sentinel (Name "NONE").
	a := newCoprocAgent("cloud_only", "ollama", "NONE")
	if _, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true}); err == nil {
		t.Error("cloud_only with absent cloud should error, not fall back to local")
	}
}

func TestCoprocCloudPrimaryPrefersLocal(t *testing.T) {
	// cloud_primary coproc prefers local; even with no cloud it runs local.
	a := newCoprocAgent("cloud_primary", "ollama", "NONE")
	r, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true})
	if err != nil || r.RoutingMetadata.ModelName != "ollama" || r.RoutingMetadata.IsCloud {
		t.Errorf("cloud_primary coproc: %+v err=%v", r.RoutingMetadata, err)
	}
}

// newCoprocAgentNoLocal builds an Agent with no LocalModel in the provider map,
// so processCoproc's pick(TierLocal) returns nil and local is treated as absent.
func newCoprocAgentNoLocal(mode, cloudName string) *Agent {
	provs := map[string]ModelProvider{
		"CloudModel": &fakeProvider{name: cloudName, out: "cloud-out"},
	}
	a := NewAgent(&fakeCoprocRouter{providers: provs}, nil)
	a.SetLocusModeGetter(func() string { return mode })
	return a
}

func TestCoprocLocalPrimaryFallsBackToCloud(t *testing.T) {
	// local_primary with no local provider must fall back to cloud and set
	// IsCloud=true and a non-empty Notice (the caller must know it fell back).
	a := newCoprocAgentNoLocal("local_primary", "anthropic")
	r, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true})
	if err != nil {
		t.Fatalf("local_primary fallback to cloud unexpected error: %v", err)
	}
	if !r.RoutingMetadata.IsCloud {
		t.Errorf("local_primary fallback: expected IsCloud=true, got %+v", r.RoutingMetadata)
	}
	if r.Notice == "" {
		t.Errorf("local_primary fallback: expected non-empty Notice, got empty (caller won't know it fell back)")
	}
}

func TestCoprocLocalOnlyHardFailsWhenAbsent(t *testing.T) {
	// local_only with no local provider must return an error — no cloud crossover allowed.
	a := newCoprocAgentNoLocal("local_only", "anthropic")
	if _, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true}); err == nil {
		t.Error("local_only with absent local should error, not fall back to cloud")
	}
}
