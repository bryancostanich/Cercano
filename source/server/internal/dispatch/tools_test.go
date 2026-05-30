package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeTool struct {
	name   string
	schema ToolSchema
	runFn  func(ctx context.Context, args json.RawMessage) (string, error)
}

func (f *fakeTool) Name() string       { return f.name }
func (f *fakeTool) Schema() ToolSchema { return f.schema }
func (f *fakeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return f.runFn(ctx, args)
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	tool := &fakeTool{name: "x", schema: ToolSchema{Name: "x", Description: "test"}}
	if err := r.Register(tool); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("x")
	if !ok {
		t.Fatal("expected tool registered")
	}
	if got.Name() != "x" {
		t.Errorf("got name %q", got.Name())
	}
}

func TestRegistry_DuplicateNameErrors(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeTool{name: "x"})
	err := r.Register(&fakeTool{name: "x"})
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("err = %q, want it to mention 'already registered'", err.Error())
	}
}

func TestRegistry_GetMissingReturnsFalse(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("nope"); ok {
		t.Fatal("expected ok=false for missing tool")
	}
}

func TestRegistry_Schemas(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeTool{name: "a", schema: ToolSchema{Name: "a"}})
	_ = r.Register(&fakeTool{name: "b", schema: ToolSchema{Name: "b"}})
	got := r.Schemas()
	if len(got) != 2 {
		t.Fatalf("got %d schemas, want 2", len(got))
	}
	names := map[string]bool{got[0].Name: true, got[1].Name: true}
	if !names["a"] || !names["b"] {
		t.Errorf("missing schema in %+v", got)
	}
}
