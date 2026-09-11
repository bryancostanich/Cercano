package llm

import (
	"context"
	"fmt"
)

// RuntimeContext identifies the observed per-request capacity of one serving
// process. A planned size is never a RuntimeContext.
type RuntimeContext struct {
	Window     int
	InstanceID string
}

type RuntimeContextProvider interface {
	RuntimeContext(context.Context, string, bool) (RuntimeContext, error)
}

// ResolveRuntimeContext only applies to the managed llama-server runtime.
// Other runtimes retain their existing capacity policies.
func ResolveRuntimeContext(ctx context.Context, p interface{ Name() string }, model string, prepare bool) (RuntimeContext, error) {
	if p == nil || p.Name() != "llama_server" {
		return RuntimeContext{}, nil
	}
	provider, ok := p.(RuntimeContextProvider)
	if !ok {
		return RuntimeContext{}, fmt.Errorf("llama-server provider does not expose runtime-confirmed capacity")
	}
	c, err := provider.RuntimeContext(ctx, model, prepare)
	if err != nil {
		return c, err
	}
	if p.Name() != "llama_server" {
		return c, nil
	}
	if c.Window <= 0 || c.InstanceID == "" {
		return RuntimeContext{}, fmt.Errorf("llama-server serving capacity is unknown for %s", model)
	}
	return c, nil
}

type runtimeContextKey struct{}

func WithRuntimeContext(ctx context.Context, c RuntimeContext) context.Context {
	return context.WithValue(ctx, runtimeContextKey{}, c)
}
func ExpectedRuntimeContext(ctx context.Context) RuntimeContext {
	c, _ := ctx.Value(runtimeContextKey{}).(RuntimeContext)
	return c
}
func CheckRuntimeContext(ctx context.Context, actual RuntimeContext) error {
	expected := ExpectedRuntimeContext(ctx)
	if expected.InstanceID != "" && (expected.InstanceID != actual.InstanceID || expected.Window != actual.Window) {
		return fmt.Errorf("llama-server instance/capacity changed after request preparation; reprepare the request")
	}
	return nil
}
