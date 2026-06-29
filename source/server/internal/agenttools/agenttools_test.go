package agenttools

import (
	"context"
	"encoding/json"
	"testing"
)

// stubTool is a minimal Tool implementation for testing the registry and
// optional-interface helpers without depending on any deleted built-in.
type stubTool struct {
	name string
	perm Permission
}

func (s stubTool) Name() string                                                   { return s.name }
func (s stubTool) Description() string                                            { return "stub" }
func (s stubTool) Permission() Permission                                         { return s.perm }
func (s stubTool) Schema() json.RawMessage                                        { return json.RawMessage(`{"type":"object"}`) }
func (s stubTool) Execute(_ context.Context, _ json.RawMessage) (*Result, error) { return nil, nil }

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	tl := stubTool{name: "alpha", perm: PermR}
	if err := r.Register(tl); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("alpha")
	if !ok || got.Name() != "alpha" {
		t.Errorf("Get(alpha): ok=%v name=%q", ok, got.Name())
	}
	if err := r.Register(tl); err == nil {
		t.Errorf("expected duplicate registration error")
	}
}

func TestRegistry_All_SortedByName(t *testing.T) {
	r := NewRegistry()
	for _, tl := range []Tool{
		stubTool{name: "Grep", perm: PermR},
		stubTool{name: "Read", perm: PermR},
		stubTool{name: "LS", perm: PermR},
	} {
		_ = r.Register(tl)
	}
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("want 3 tools, got %d", len(all))
	}
	want := []string{"Grep", "LS", "Read"}
	for i, n := range want {
		if all[i].Name() != n {
			t.Errorf("position %d: want %q got %q", i, n, all[i].Name())
		}
	}
}

func TestRegistry_Filter(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(stubTool{name: "r1", perm: PermR})
	r.MustRegister(stubTool{name: "r2", perm: PermR})
	r.MustRegister(stubTool{name: "w1", perm: PermW})
	r.MustRegister(stubTool{name: "x1", perm: PermX})
	all := r.All()
	rTier := r.Filter(PermR)
	wTier := r.Filter(PermW)
	xTier := r.Filter(PermX)
	if len(rTier)+len(wTier)+len(xTier) != len(all) {
		t.Errorf("R+W+X (%d+%d+%d) should sum to All (%d)",
			len(rTier), len(wTier), len(xTier), len(all))
	}
	for _, tier := range []struct {
		name  string
		tools []Tool
	}{{"R", rTier}, {"W", wTier}, {"X", xTier}} {
		if len(tier.tools) == 0 {
			t.Errorf("expected at least one %s-tier tool", tier.name)
		}
	}
}

func TestOriginOfDefaultsBuiltin(t *testing.T) {
	tl := stubTool{name: "s", perm: PermR}
	if got := OriginOf(tl); got != OriginBuiltin {
		t.Fatalf("tool without Originer: origin = %q, want builtin", got)
	}
}

// fakeMCP wraps a stubTool and overrides Origin / Destructive via the optional
// interfaces, testing that OriginOf and IsDestructive honour them.
type fakeMCP struct{ stubTool }

func (fakeMCP) Origin() Origin    { return OriginMCP }
func (fakeMCP) Destructive() bool { return true }

func TestOriginOfHonorsOptionalInterface(t *testing.T) {
	if got := OriginOf(fakeMCP{}); got != OriginMCP {
		t.Fatalf("origin = %q, want mcp", got)
	}
}

func TestIsDestructiveDefaultsFalse(t *testing.T) {
	tl := stubTool{name: "s", perm: PermR}
	if IsDestructive(tl) {
		t.Fatal("tool without Destructiver must not be destructive")
	}
}

func TestIsDestructiveHonorsOptionalInterface(t *testing.T) {
	if !IsDestructive(fakeMCP{}) {
		t.Fatal("tool implementing Destructiver()=true should report destructive")
	}
}
