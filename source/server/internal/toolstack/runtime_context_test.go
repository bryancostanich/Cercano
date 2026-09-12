package toolstack_test

import (
	"cercano/source/server/internal/dispatch"
	tools "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/toolstack"
	"cercano/source/server/pkg/config"
	"context"
	"fmt"
	"testing"
)

type runtimeTargetProvider struct {
	inference.Provider
	prepares int
}

func (p *runtimeTargetProvider) Name() string { return "llama_server" }
func (p *runtimeTargetProvider) RuntimeContext(_ context.Context, model string, prepare bool) (llm.RuntimeContext, error) {
	if prepare {
		p.prepares++
	}
	n := 65536
	if model == "small" {
		n = 8192
	}
	if model == "missing" {
		return llm.RuntimeContext{}, fmt.Errorf("unconfirmed")
	}
	return llm.RuntimeContext{Window: n, InstanceID: model + "-process"}, nil
}

func TestCapabilityBudgetUsesPreparedRuntimeTarget(t *testing.T) {
	provider := &runtimeTargetProvider{}
	selections := 0
	e := toolstack.NewEngine(toolstack.EngineDeps{Providers: func() inference.Tiers { selections++; return inference.Tiers{Open: provider} }, LocusMode: func() locus.Mode { return locus.OpenOnly }, ModelFor: func(bool, config.Tier) string { return "large" }})
	svc := tools.New(nil, nil, nil, nil)
	svc.SetEngine(e)
	n := 8192
	cfg := config.Config{OpenRuntime: "llama_server", LlamaServer: config.LlamaServerConfig{ContextSize: &n}}
	toolstack.InstallCapabilities(svc, toolstack.CapDeps{Config: &cfg})
	services := svc.CapRegistry().Services()
	target, err := services.DispatchTarget(t.Context(), dispatch.Spec{LocalOffload: true, Role: dispatch.RoleCoproc})
	if err != nil || target.ContextWindow != 65536 || !target.ContextWindowKnown || target.Model != "large" {
		t.Fatalf("target=%+v err=%v", target, err)
	}
	if selections != 1 || provider.prepares != 1 {
		t.Fatalf("selection/preparation count=%d/%d", selections, provider.prepares)
	}
	target, err = services.DispatchTarget(t.Context(), dispatch.Spec{LocalOffload: true, Role: dispatch.RoleCoproc, ModelOverride: "small"})
	if err != nil || target.ContextWindow != 8192 {
		t.Fatalf("model override target=%+v %v", target, err)
	}
	if _, err := services.DispatchTarget(t.Context(), dispatch.Spec{LocalOffload: true, Role: dispatch.RoleCoproc, ModelOverride: "missing"}); err == nil {
		t.Fatal("unknown capacity authorized a research budget")
	}
}
