package modelevidence

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/catalog"
	"cercano/source/server/internal/modelmetadata"
)

type fakeSource struct {
	name    string
	details map[string]catalog.Detail
	err     error
	calls   int
	block   chan struct{}
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) List(context.Context, catalog.ListOptions) ([]catalog.Model, error) {
	return nil, nil
}

func (f *fakeSource) Detail(ctx context.Context, id string) (catalog.Detail, error) {
	f.calls++
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return catalog.Detail{}, ctx.Err()
		}
	}
	if f.err != nil {
		return catalog.Detail{}, f.err
	}
	d, ok := f.details[id]
	if !ok {
		return catalog.Detail{}, errors.New("unknown model")
	}
	return d, nil
}

type fakeRegistry struct{ src *fakeSource }

func (r fakeRegistry) Lookup(name string) (catalog.Source, bool) {
	if r.src != nil && r.src.name == name {
		return r.src, true
	}
	return nil, false
}

func deepinfraSource(details map[string]catalog.Detail) *fakeSource {
	return &fakeSource{name: "deepinfra", details: details}
}

func deepinfraID(model string) modelmetadata.Identity {
	return modelmetadata.Identity{
		Provider: "deepinfra",
		BaseURL:  "https://api.deepinfra.com/v1/openai",
		Model:    model,
	}
}

func TestResolve_UsesDiscoveredContextAndVision(t *testing.T) {
	src := deepinfraSource(map[string]catalog.Detail{
		"zai-org/GLM-5.3-Flash": {ContextLength: 1048576, SupportsVision: true},
	})
	r := New(fakeRegistry{src})
	ev := r.Resolve(context.Background(), deepinfraID("zai-org/GLM-5.3-Flash"))
	if ev.ContextWindow != 1048576 {
		t.Fatalf("context window = %d, want 1048576", ev.ContextWindow)
	}
	if ev.Vision != modelmetadata.VisionSupported {
		t.Fatalf("vision = %q, want supported", ev.Vision)
	}
}

// A source that publishes no vision signal must leave vision UNKNOWN. Reading
// absence as "unsupported" would be as unfounded as reading it as "supported".
func TestResolve_AbsentVisionSignalStaysUnknown(t *testing.T) {
	src := deepinfraSource(map[string]catalog.Detail{
		"openai/gpt-oss-120b": {ContextLength: 131072},
	})
	r := New(fakeRegistry{src})
	ev := r.Resolve(context.Background(), deepinfraID("openai/gpt-oss-120b"))
	if ev.Vision != modelmetadata.VisionUnknown {
		t.Fatalf("vision = %q, want unknown", ev.Vision)
	}
	if ev.ContextWindow != 131072 {
		t.Fatalf("context window = %d, want 131072", ev.ContextWindow)
	}
}

func TestResolve_UnknownModelYieldsNoEvidence(t *testing.T) {
	r := New(fakeRegistry{deepinfraSource(nil)})
	ev := r.Resolve(context.Background(), deepinfraID("fixture/never-heard-of-it"))
	if ev.ContextWindow != 0 || ev.Vision != modelmetadata.VisionUnknown {
		t.Fatalf("got %+v, want zero evidence", ev)
	}
}

// Identity, not model name, is the key: the same id on a different endpoint
// must not inherit the first provider's evidence.
func TestResolve_IdentityIsolatesProviders(t *testing.T) {
	src := deepinfraSource(map[string]catalog.Detail{
		"zai-org/GLM-5.3-Flash": {ContextLength: 1048576, SupportsVision: true},
	})
	r := New(fakeRegistry{src})
	other := modelmetadata.Identity{
		Provider: "someone-else",
		BaseURL:  "https://gateway.example.com/v1",
		Model:    "zai-org/GLM-5.3-Flash",
	}
	ev := r.Resolve(context.Background(), other)
	if ev.Vision != modelmetadata.VisionUnknown || ev.ContextWindow != 0 {
		t.Fatalf("foreign endpoint inherited evidence: %+v", ev)
	}
}

func TestResolve_InvalidUpstreamValuesAreNormalized(t *testing.T) {
	src := deepinfraSource(map[string]catalog.Detail{
		"fixture/negative": {ContextLength: -5},
	})
	r := New(fakeRegistry{src})
	ev := r.Resolve(context.Background(), deepinfraID("fixture/negative"))
	if ev.ContextWindow != 0 {
		t.Fatalf("negative capacity survived normalization: %d", ev.ContextWindow)
	}
}

func TestResolve_DiscoveryFailureLeavesEverythingUnknown(t *testing.T) {
	src := deepinfraSource(nil)
	src.err = errors.New("index unreachable")
	r := New(fakeRegistry{src})
	ev := r.Resolve(context.Background(), deepinfraID("zai-org/GLM-5.3-Flash"))
	if ev.ContextWindow != 0 || ev.Vision != modelmetadata.VisionUnknown {
		t.Fatalf("discovery failure produced evidence: %+v", ev)
	}
}

func TestResolve_SlowSourceIsBounded(t *testing.T) {
	src := deepinfraSource(nil)
	src.block = make(chan struct{})
	defer close(src.block)
	r := New(fakeRegistry{src}, WithTimeout(20*time.Millisecond))
	done := make(chan modelmetadata.Evidence, 1)
	go func() { done <- r.Resolve(context.Background(), deepinfraID("slow/model")) }()
	select {
	case ev := <-done:
		if ev.ContextWindow != 0 || ev.Vision != modelmetadata.VisionUnknown {
			t.Fatalf("timed-out lookup produced evidence: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resolve did not honor its timeout")
	}
}

func TestResolve_CachesPerIdentity(t *testing.T) {
	src := deepinfraSource(map[string]catalog.Detail{
		"a/model": {ContextLength: 1000},
	})
	r := New(fakeRegistry{src})
	for i := 0; i < 3; i++ {
		r.Resolve(context.Background(), deepinfraID("a/model"))
	}
	if src.calls != 1 {
		t.Fatalf("source consulted %d times, want 1 (cached)", src.calls)
	}
}

func TestResolve_CacheExpires(t *testing.T) {
	src := deepinfraSource(map[string]catalog.Detail{"a/model": {ContextLength: 1000}})
	now := time.Unix(0, 0)
	r := New(fakeRegistry{src}, WithTTL(time.Minute), WithClock(func() time.Time { return now }))
	r.Resolve(context.Background(), deepinfraID("a/model"))
	now = now.Add(2 * time.Minute)
	r.Resolve(context.Background(), deepinfraID("a/model"))
	if src.calls != 2 {
		t.Fatalf("source consulted %d times, want 2 (TTL expiry)", src.calls)
	}
}

// Tightening image routing must not withdraw vision from vendors where it is
// verified today.
func TestResolve_ShippedVendorsKeepConfirmedVision(t *testing.T) {
	r := New(nil)
	cases := []struct {
		name string
		id   modelmetadata.Identity
		want modelmetadata.Vision
	}{
		{"anthropic direct", modelmetadata.Identity{Provider: "anthropic", Model: "claude-sonnet-4-6"}, modelmetadata.VisionSupported},
		{"openai 4o", modelmetadata.Identity{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o"}, modelmetadata.VisionSupported},
		{"openai 3.5 text-only", modelmetadata.Identity{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-3.5-turbo"}, modelmetadata.VisionUnsupported},
		{"gemini", modelmetadata.Identity{Provider: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Model: "gemini-2.5-pro"}, modelmetadata.VisionSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Resolve(context.Background(), tc.id).Vision; got != tc.want {
				t.Fatalf("vision = %q, want %q", got, tc.want)
			}
		})
	}
}

// A gateway that merely names a model "gpt-4o" carries none of OpenAI's
// guarantees.
func TestResolve_ForeignGatewayDoesNotInheritVendorCapability(t *testing.T) {
	r := New(nil)
	id := modelmetadata.Identity{Provider: "custom", BaseURL: "https://gateway.example.com/v1", Model: "gpt-4o"}
	if got := r.Resolve(context.Background(), id).Vision; got != modelmetadata.VisionUnknown {
		t.Fatalf("vision = %q, want unknown", got)
	}
}

// A provider's published capacity must beat the model-name family heuristic.
// The family table matches "qwen" and answers 131072; DeepInfra serves this
// model with 262144. Preferring the heuristic here would silently shrink the
// budget for every request against it.
func TestResolve_PublishedCapacityBeatsFamilyHeuristic(t *testing.T) {
	src := deepinfraSource(map[string]catalog.Detail{
		"Qwen/Qwen3.8-2.4T-A95B": {ContextLength: 262144},
	})
	r := New(fakeRegistry{src})
	if got := r.Resolve(context.Background(), deepinfraID("Qwen/Qwen3.8-2.4T-A95B")).ContextWindow; got != 262144 {
		t.Fatalf("context window = %d, want the published 262144", got)
	}
}

// Shipped knowledge carries vision only. Capacity for vendors without an index
// stays zero here, and callers fall back to the conventional window — the
// behavior that existed before this resolver.
func TestResolve_ShippedKnowledgeSuppliesVisionNotCapacity(t *testing.T) {
	r := New(nil)
	ev := r.Resolve(context.Background(), modelmetadata.Identity{Provider: "anthropic", Model: "claude-sonnet-4-6"})
	if ev.Vision != modelmetadata.VisionSupported {
		t.Fatalf("vision = %v, want supported", ev.Vision)
	}
	if ev.ContextWindow != 0 {
		t.Fatalf("context window = %d; shipped knowledge must not override the conventional table", ev.ContextWindow)
	}
}

func TestSnapshot_RecordsUnknownExplicitly(t *testing.T) {
	src := deepinfraSource(map[string]catalog.Detail{
		"zai-org/GLM-5.3-Flash": {ContextLength: 1048576, SupportsVision: true},
	})
	r := New(fakeRegistry{src})
	snap := r.Snapshot(context.Background(), []modelmetadata.Identity{
		deepinfraID("zai-org/GLM-5.3-Flash"),
		deepinfraID("fixture/unknown"),
		{Model: ""},                          // skipped
		deepinfraID("zai-org/GLM-5.3-Flash"), // deduped
	})
	if len(snap) != 2 {
		t.Fatalf("snapshot has %d entries, want 2: %+v", len(snap), snap)
	}
	ev, ok := snap.Lookup(deepinfraID("fixture/unknown"))
	if !ok {
		t.Fatal("unknown identity missing from snapshot; worker cannot distinguish 'looked and found nothing' from 'never considered'")
	}
	if ev.Vision != modelmetadata.VisionUnknown || ev.ContextWindow != 0 {
		t.Fatalf("unknown entry carries evidence: %+v", ev)
	}
}
