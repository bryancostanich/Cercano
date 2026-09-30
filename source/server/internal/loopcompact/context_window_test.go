package loopcompact

import (
	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
	"context"
	"testing"
)

func TestDispatchUsesExecutingWindowNotLocalSummarizerWindow(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.Compaction.Enabled = true
	localProbes := 0
	p := &policyProvider{name: "secondary"}
	factory := NewFactory(WiringDeps{Cfg: cfg, ChatModel: "unrelated-local", Candidates: func() inference.Tiers {
		return inference.Tiers{Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: p, IsCloud: true}}}
	}, OpenRuntimeContext: func(context.Context, string, bool) (llm.RuntimeContext, error) {
		localProbes++
		return llm.RuntimeContext{Window: 32768}, nil
	}})
	c := factory().(*Compactor)
	ctx := agent.WithLoopCompactionScope(t.Context(), agent.LoopCompactionScope{ContextWindow: 1048576, ContextWindowKnown: true})
	_, _, err := c.CompactLoopHistory(ctx, bigHistory(60, 100))
	if err != nil {
		t.Fatal(err)
	}
	if localProbes != 0 {
		t.Fatalf("unrelated local probes=%d", localProbes)
	}
	if c.cfg.CompactedBudgetTokens != 314572 || c.cfg.ActivationFloorTokens != 40000 {
		t.Fatalf("wrong executing window policy: %+v", c.cfg)
	}
}

func TestContextSizingChangesWithRouteWithoutChangingExplicitThreshold(t *testing.T) {
	cfg := config.Config{}
	cfg.Compaction.Enabled = true
	cfg.Compaction.ActivationFloorTokens = 12345
	cfg.Compaction.CompactedBudgetPct = .25
	factory := NewFactory(WiringDeps{Cfg: cfg, Candidates: func() inference.Tiers { return inference.Tiers{} }})
	c := factory().(*Compactor)
	hist := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "tiny"}}}}
	for _, tc := range []struct {
		window int
		known  bool
		want   int
	}{{1048576, true, 262144}, {32768, true, CompactedBudgetFloorTokens}, {0, false, CompactedBudgetFloorTokens}} {
		ctx := agent.WithLoopCompactionScope(t.Context(), agent.LoopCompactionScope{ContextWindow: tc.window, ContextWindowKnown: tc.known})
		if _, _, err := c.CompactLoopHistory(ctx, hist); err != nil {
			t.Fatal(err)
		}
		if c.cfg.CompactedBudgetTokens != tc.want || c.cfg.ActivationFloorTokens != 12345 {
			t.Fatalf("policy=%+v", c.cfg)
		}
	}
	other := factory().(*Compactor)
	if other.cfg.CompactedBudgetTokens != CompactedBudgetFloorTokens {
		t.Fatal("budget leaked across dispatches")
	}
}
