package capabilities

import (
	"context"
	"testing"
)

type fakeCap struct {
	name string
	surf Surface
}

func (c fakeCap) Name() string        { return c.name }
func (c fakeCap) Description() string  { return "fake " + c.name }
func (c fakeCap) Tier() Tier           { return TierR }
func (c fakeCap) Schema() Schema       { return Schema(`{"type":"object"}`) }
func (c fakeCap) Surfaces() Surface    { return c.surf }
func (c fakeCap) Execute(context.Context, *Call) (*Result, error) {
	return NewTextResult("ok"), nil
}

func TestRegistryRegisterGet(t *testing.T) {
	r := NewRegistry(Services{})
	if err := r.Register(fakeCap{name: "a", surf: SurfaceAgent | SurfaceMCP}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(fakeCap{name: "a"}); err == nil {
		t.Fatal("duplicate name should error")
	}
	got, ok := r.Get("a")
	if !ok || got.Name() != "a" {
		t.Fatal("Get failed")
	}
}

func TestRegistryForSurface(t *testing.T) {
	r := NewRegistry(Services{})
	r.MustRegister(fakeCap{name: "agentonly", surf: SurfaceAgent})
	r.MustRegister(fakeCap{name: "both", surf: SurfaceAgent | SurfaceMCP})
	if got := len(r.ForSurface(SurfaceMCP)); got != 1 {
		t.Fatalf("ForSurface(MCP) = %d, want 1", got)
	}
	if got := len(r.ForSurface(SurfaceAgent)); got != 2 {
		t.Fatalf("ForSurface(Agent) = %d, want 2", got)
	}
}
