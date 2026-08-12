package toolstack_test

import (
	"context"
	"testing"

	"cercano/source/server/internal/capabilities"
	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/toolstack"
	"cercano/source/server/pkg/config"
)

// stubVision is a no-op capabilities.VisionService used to prove Services wiring.
type stubVision struct{}

func (stubVision) Available() bool         { return true }
func (stubVision) Lookup(_, _ string) bool { return false }
func (stubVision) Inspect(context.Context, string, string, string) (capabilities.VisionAnswer, error) {
	return capabilities.VisionAnswer{}, nil
}

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

// TestInstallCapabilities_WiresVision verifies a supplied Vision service reaches
// Services.Vision, and that omitting it leaves Vision nil (so inspect_image
// reports unavailable rather than crashing).
func TestInstallCapabilities_WiresVision(t *testing.T) {
	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() inference.Tiers { return inference.Tiers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
	})

	// With a Vision service supplied.
	svc := toolssvc.New(nil, nil, nil, nil)
	svc.SetEngine(eng)
	toolstack.InstallCapabilities(svc, toolstack.CapDeps{Vision: stubVision{}})
	if svc.CapRegistry().Services().Vision == nil {
		t.Fatal("Services.Vision is nil despite a supplied vision service")
	}

	// Without one — Vision stays nil (inspect_image degrades gracefully).
	svc2 := toolssvc.New(nil, nil, nil, nil)
	svc2.SetEngine(eng)
	toolstack.InstallCapabilities(svc2, toolstack.CapDeps{})
	if svc2.CapRegistry().Services().Vision != nil {
		t.Fatal("Services.Vision should be nil when no vision service is supplied")
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
