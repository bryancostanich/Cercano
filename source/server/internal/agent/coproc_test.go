package agent

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
)

// fakeLLMProvider is a minimal llm.Provider for dispatch engine tests.
type fakeLLMProvider struct {
	name string
	out  string
}

func (f *fakeLLMProvider) Name() string                   { return f.name }
func (f *fakeLLMProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (f *fakeLLMProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{
		Blocks:       []llm.Block{{Type: llm.BlockText, Text: f.out}},
		InputTokens:  1,
		OutputTokens: 1,
	}, nil
}
func (f *fakeLLMProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

// newDispatchCoprocAgent builds an Agent with a dispatch.Engine backed by fake
// llm.Providers. localModel/cloudModel are the model name strings returned by
// SetModelFor (they appear in RoutingMetadata.ModelName). A nil local or cloud
// provider means that tier is absent.
func newDispatchCoprocAgent(modeStr string, localProv, cloudProv *fakeLLMProvider) *Agent {
	var localModel, cloudModel string
	if localProv != nil {
		localModel = localProv.name
	}
	if cloudProv != nil {
		cloudModel = cloudProv.name
	}

	var dLocal, dCloud llm.Provider
	if localProv != nil {
		dLocal = localProv
	}
	if cloudProv != nil {
		dCloud = cloudProv
	}

	modeFn := func() locus.Mode {
		m, _ := locus.ParseMode(modeStr)
		return m
	}
	eng := dispatch.NewEngine(dispatch.Providers{Local: dLocal, Cloud: dCloud}, modeFn, nil)
	eng.SetModelFor(func(isCloud bool) string {
		if isCloud {
			return cloudModel
		}
		return localModel
	})

	// Agent still needs a Router (for non-coproc paths); use a stub.
	a := NewAgent(&fakeCoprocRouter{providers: map[string]ModelProvider{}}, nil)
	a.SetLocusModeGetter(func() string { return modeStr })
	a.SetDispatchEngine(eng)
	return a
}

// fakeCoprocRouter satisfies the Router interface for test Agents that only
// exercise the coproc path (router not touched by processCoproc).
type fakeCoprocRouter struct{ providers map[string]ModelProvider }

func (r *fakeCoprocRouter) ClassifyIntent(req *Request) (Intent, error) {
	return IntentChat, nil
}
func (r *fakeCoprocRouter) SelectProvider(req *Request, i Intent) (ModelProvider, error) {
	return nil, nil
}
func (r *fakeCoprocRouter) GetModelProviders() map[string]ModelProvider { return r.providers }

func TestCoprocRoutesPerMode(t *testing.T) {
	ctx := context.Background()

	// local_primary → local
	r, err := newDispatchCoprocAgent("local_primary",
		&fakeLLMProvider{name: "ollama", out: "local-out"},
		&fakeLLMProvider{name: "anthropic", out: "cloud-out"},
	).ProcessRequest(ctx, &Request{Input: "x", Coproc: true})
	if err != nil || r.RoutingMetadata.ModelName != "ollama" || r.RoutingMetadata.IsCloud {
		t.Errorf("local_primary coproc: %+v err=%v", r.RoutingMetadata, err)
	}

	// cloud_only → cloud
	r, err = newDispatchCoprocAgent("cloud_only",
		&fakeLLMProvider{name: "ollama", out: "local-out"},
		&fakeLLMProvider{name: "anthropic", out: "cloud-out"},
	).ProcessRequest(ctx, &Request{Input: "x", Coproc: true})
	if err != nil || r.RoutingMetadata.ModelName != "anthropic" || !r.RoutingMetadata.IsCloud {
		t.Errorf("cloud_only coproc: %+v err=%v", r.RoutingMetadata, err)
	}
}

func TestCoprocCloudOnlyHardFailsWhenAbsent(t *testing.T) {
	// cloud provider is absent (nil) under cloud_only → hard error, no fallback.
	a := newDispatchCoprocAgent("cloud_only",
		&fakeLLMProvider{name: "ollama", out: "local-out"},
		nil,
	)
	if _, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true}); err == nil {
		t.Error("cloud_only with absent cloud should error, not fall back to local")
	}
}

func TestCoprocCloudPrimaryPrefersLocal(t *testing.T) {
	// cloud_primary coproc prefers local; even with no cloud it runs local.
	a := newDispatchCoprocAgent("cloud_primary",
		&fakeLLMProvider{name: "ollama", out: "local-out"},
		nil,
	)
	r, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true})
	if err != nil || r.RoutingMetadata.ModelName != "ollama" || r.RoutingMetadata.IsCloud {
		t.Errorf("cloud_primary coproc: %+v err=%v", r.RoutingMetadata, err)
	}
}

func TestCoprocLocalPrimaryFallsBackToCloud(t *testing.T) {
	// local_primary with no local provider must fall back to cloud and set
	// IsCloud=true and a non-empty Notice (the caller must know it fell back).
	a := newDispatchCoprocAgent("local_primary",
		nil,
		&fakeLLMProvider{name: "anthropic", out: "cloud-out"},
	)
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
	if !strings.Contains(r.Notice, "preferred co-processor tier unavailable") {
		t.Errorf("local_primary fallback: Notice missing expected text: %q", r.Notice)
	}
}

func TestCoprocLocalOnlyHardFailsWhenAbsent(t *testing.T) {
	// local_only with no local provider must return an error — no cloud crossover allowed.
	a := newDispatchCoprocAgent("local_only",
		nil,
		&fakeLLMProvider{name: "anthropic", out: "cloud-out"},
	)
	if _, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true}); err == nil {
		t.Error("local_only with absent local should error, not fall back to cloud")
	}
}
