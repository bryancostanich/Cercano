package toolstack_test

import (
	"testing"

	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/toolstack"
	"cercano/source/server/pkg/config"
)

// TestInstallCapabilities_WiresDispatch pins the invariant whose absence was the
// bug: after the shared builder runs, the capability registry's Services.Dispatch
// is non-nil, so a capability that routes model work through the dispatch engine
// (local, the co-processor caps, review, the web caps, the dispatch sub-agent)
// can reach a live engine. The worker previously handed capabilities an empty
// Services{}, so this was nil and every such call failed at runtime.
func TestInstallCapabilities_WiresDispatch(t *testing.T) {
	svc := toolssvc.New(nil, nil, nil, nil)

	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() inference.Tiers { return inference.Tiers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
		ModelFor:  func(bool, config.Tier) string { return "model" },
	})
	svc.SetEngine(eng)

	toolstack.InstallCapabilities(svc, toolstack.CapDeps{})

	if svc.CapRegistry() == nil {
		t.Fatal("capability registry is nil")
	}
	if svc.CapRegistry().Services().Dispatch == nil {
		t.Fatal("Services.Dispatch is nil — capabilities cannot reach the dispatch engine")
	}
	if svc.Registry() == nil || len(svc.Registry().All()) == 0 {
		t.Fatal("agent tool registry is empty — builtins were not registered")
	}
}

// TestNewEngine_NilOptionalsSafe verifies NewEngine tolerates nil ModelFor and
// UsageSink (both optional) without panicking.
func TestNewEngine_NilOptionalsSafe(t *testing.T) {
	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() inference.Tiers { return inference.Tiers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
	})
	if eng == nil {
		t.Fatal("NewEngine returned nil")
	}
}
