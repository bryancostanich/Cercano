package agent

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

// TestCoprocDispatchOpenPick verifies that local_primary routes coproc to local.
func TestCoprocDispatchOpenPick(t *testing.T) {
	local := &fakeLLMProvider{name: "ollama", out: "local response"}
	cloud := &fakeLLMProvider{name: "anthropic", out: "cloud response"}
	a := newDispatchCoprocAgent("open_primary", local, cloud)

	r, err := a.ProcessRequest(context.Background(), &Request{Input: "hello", Coproc: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.RoutingMetadata.ModelName != "ollama" {
		t.Errorf("ModelName=%q, want %q", r.RoutingMetadata.ModelName, "ollama")
	}
	if r.RoutingMetadata.IsCloud {
		t.Error("IsCloud=true, want false for local_primary coproc")
	}
	if r.RoutingMetadata.Confidence != 1.0 {
		t.Errorf("Confidence=%v, want 1.0", r.RoutingMetadata.Confidence)
	}
	if r.Output == "" {
		t.Error("Output is empty")
	}
}

// TestLegacyOneShotAssignedPrimaryUsesCloud verifies cloud_primary coproc stays local.
func TestLegacyOneShotAssignedPrimaryUsesCloud(t *testing.T) {
	local := &fakeLLMProvider{name: "ollama", out: "local response"}
	cloud := &fakeLLMProvider{name: "anthropic", out: "cloud response"}
	a := newDispatchCoprocAgent("cloud_primary", local, cloud)

	r, err := a.ProcessRequest(context.Background(), &Request{Input: "hello", Coproc: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.RoutingMetadata.IsCloud {
		t.Errorf("saved Primary assignment should use cloud")
	}
	if r.RoutingMetadata.ModelName != "anthropic" {
		t.Errorf("ModelName=%q, want %q", r.RoutingMetadata.ModelName, "anthropic")
	}
}

// TestCoprocDispatchFallbackNotice verifies that a fallback sets the Notice.
func TestCoprocDispatchFallbackNotice(t *testing.T) {
	// local_primary, no local available → falls back to cloud.
	cloud := &fakeLLMProvider{name: "anthropic", out: "cloud response"}
	a := newDispatchCoprocAgent("open_primary", nil, cloud)

	r, err := a.ProcessRequest(context.Background(), &Request{Input: "hello", Coproc: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(r.Notice, "preferred main tier unavailable") {
		t.Errorf("Notice missing expected text: %q", r.Notice)
	}
	if !r.RoutingMetadata.IsCloud {
		t.Error("IsCloud=false, want true for cloud fallback")
	}
}

// TestCoprocDispatchNoProviderError verifies a hard error when no provider available.
func TestCoprocDispatchNoProviderError(t *testing.T) {
	eng := dispatch.NewEngine(
		func() dispatch.Providers { return dispatch.Providers{} }, // both nil
		func() locus.Mode { return locus.OpenOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool, _ config.Tier) string { return "local-model" })

	a := NewAgent(&fakeCoprocRouter{providers: map[string]TurnRunner{}}, nil)
	a.SetLocusModeGetter(func() string { return "open_only" })
	a.SetDispatchEngine(eng)

	_, err := a.ProcessRequest(context.Background(), &Request{Input: "hello", Coproc: true})
	if err == nil {
		t.Fatal("expected error when no provider available")
	}
	if !strings.Contains(err.Error(), "destination secondary unavailable") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestCoprocDispatchModelOverride verifies ModelOverride reflected in RoutingMetadata.ModelName.
func TestCoprocDispatchModelOverride(t *testing.T) {
	local := &fakeLLMProvider{name: "ollama", out: "local response"}
	a := newDispatchCoprocAgent("open_only", local, nil)

	const override = "my-special-model"
	r, err := a.ProcessRequest(context.Background(), &Request{
		Input:         "hello",
		Coproc:        true,
		ModelOverride: override,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.RoutingMetadata.ModelName != override {
		t.Errorf("ModelName=%q, want %q (ModelOverride not reflected)", r.RoutingMetadata.ModelName, override)
	}
}

// TestCoprocDispatchNoEngine verifies a clear error when engine not configured.
func TestCoprocDispatchNoEngine(t *testing.T) {
	a := NewAgent(&fakeCoprocRouter{providers: map[string]TurnRunner{}}, nil)
	// Deliberately do NOT call SetDispatchEngine.

	_, err := a.ProcessRequest(context.Background(), &Request{Input: "hello", Coproc: true})
	if err == nil {
		t.Fatal("expected error when engine not configured")
	}
	if !strings.Contains(err.Error(), "co-processor dispatch engine not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestCoprocDispatchTokensPopulated verifies input/output token counts propagate.
func TestCoprocDispatchTokensPopulated(t *testing.T) {
	local := &fakeLLMProvider{name: "ollama", out: "some output"}
	a := newDispatchCoprocAgent("open_only", local, nil)

	r, err := a.ProcessRequest(context.Background(), &Request{Input: "hello", Coproc: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.InputTokens == 0 || r.OutputTokens == 0 {
		t.Errorf("tokens not propagated: input=%d output=%d", r.InputTokens, r.OutputTokens)
	}
}

func TestLegacyOneShotUsesDefaultDispatchClass(t *testing.T) {
	local := &fakeLLMProvider{name: "local", out: "local"}
	secondary := &fakeLLMProvider{name: "secondary", out: "secondary"}
	eng := dispatch.NewEngine(func() dispatch.Providers {
		return dispatch.Providers{Open: local, Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: secondary, IsCloud: true}}}
	}, func() locus.Mode { return locus.CloudPrimary }, nil)
	eng.SetDestinationModelFor(func(sel inference.Selection, tier config.Tier) string {
		if tier != config.TierMostCapable {
			t.Errorf("tier=%s", tier)
		}
		return sel.Provider.Name()
	})
	a := NewAgent(&fakeCoprocRouter{}, nil)
	a.SetDispatchEngine(eng)
	res, err := a.ProcessRequest(context.Background(), &Request{Input: "fixture", Coproc: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.RoutingMetadata.ModelName != "secondary" || !res.RoutingMetadata.IsCloud {
		t.Fatalf("legacy request bypassed Default dispatch: %+v", res)
	}
}
