package agentadapter

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
)

type echoCap struct{}

func (echoCap) Name() string       { return "read_file" }
func (echoCap) Description() string { return "echo" }
func (echoCap) Tier() capabilities.Tier { return capabilities.TierR }
func (echoCap) Schema() capabilities.Schema { return capabilities.Schema(`{"type":"object"}`) }
func (echoCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (echoCap) Execute(_ context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	return capabilities.NewTextResult("hello " + string(call.Args)), nil
}

func TestAsToolAppliesAliasAndTier(t *testing.T) {
	tool := AsTool(echoCap{}, "Read")
	if tool.Name() != "Read" {
		t.Fatalf("display name = %q, want Read", tool.Name())
	}
	if tool.Permission() != agenttools.PermR {
		t.Fatalf("permission = %q, want R", tool.Permission())
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != agenttools.ResultText || res.Text == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestBuildAgentRegistryUsesAgentSurfaceOnly(t *testing.T) {
	reg := capabilities.NewRegistry(capabilities.Services{})
	reg.MustRegister(echoCap{}) // agent-only
	ar := BuildAgentRegistry(reg, AliasMap{"read_file": "Read"}, nil)
	if _, ok := ar.Get("Read"); !ok {
		t.Fatal("expected Read in agent registry")
	}
}

func TestBuildAgentRegistryRegistersSynonyms(t *testing.T) {
	reg := capabilities.NewRegistry(capabilities.Services{})
	reg.MustRegister(echoCap{}) // canonical "read_file"
	ar := BuildAgentRegistry(
		reg,
		AliasMap{"read_file": "Read"},
		SynonymMap{"read_file": {"open_file"}},
	)
	if _, ok := ar.Get("Read"); !ok {
		t.Fatal("primary display 'Read' missing")
	}
	if _, ok := ar.Get("open_file"); !ok {
		t.Fatal("synonym 'open_file' missing")
	}
}

func TestBuildAgentRegistrySkipsSynonymCollidingWithPrimary(t *testing.T) {
	reg := capabilities.NewRegistry(capabilities.Services{})
	reg.MustRegister(echoCap{})
	// Synonym equals primary display => must be skipped, not double-registered.
	ar := BuildAgentRegistry(
		reg,
		AliasMap{"read_file": "Read"},
		SynonymMap{"read_file": {"Read"}},
	)
	if _, ok := ar.Get("Read"); !ok {
		t.Fatal("primary display 'Read' missing")
	}
}
