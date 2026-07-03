package agentadapter

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
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
	tool := AsTool(echoCap{}, "Read", capabilities.Services{})
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

// spyCap records the Call it received so tests can assert Services plumbing.
type spyCap struct{ got *capabilities.Call }

func (*spyCap) Name() string                { return "spy" }
func (*spyCap) Description() string         { return "spy" }
func (*spyCap) Tier() capabilities.Tier     { return capabilities.TierR }
func (*spyCap) Schema() capabilities.Schema { return capabilities.Schema(`{"type":"object"}`) }
func (*spyCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent
}
func (s *spyCap) Execute(_ context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	s.got = call
	return capabilities.NewTextResult("ok"), nil
}

// TestBuildAgentRegistryPlumbsServices guards against regressing the bug where
// capTool.Execute built a Call without Svc, silently zeroing Services and
// breaking dispatch/workflow, review, and the coproc capabilities.
func TestBuildAgentRegistryPlumbsServices(t *testing.T) {
	sentinel := make(chan struct{}, 1)
	svc := capabilities.Services{
		Dispatch: func(context.Context, dispatch.Spec) (dispatch.Result, error) {
			sentinel <- struct{}{}
			return dispatch.Result{}, nil
		},
	}
	spy := &spyCap{}
	reg := capabilities.NewRegistry(svc)
	reg.MustRegister(spy)
	ar := BuildAgentRegistry(reg, nil, nil)

	tool, ok := ar.Get("spy")
	if !ok {
		t.Fatal("spy tool missing")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if spy.got == nil {
		t.Fatal("capability did not record the Call")
	}
	if spy.got.Svc.Dispatch == nil {
		t.Fatal("Services.Dispatch was nil at Execute; agentadapter is dropping Services")
	}
	if _, err := spy.got.Svc.Dispatch(context.Background(), dispatch.Spec{}); err != nil {
		t.Fatalf("Dispatch closure: %v", err)
	}
	select {
	case <-sentinel:
	default:
		t.Fatal("registry's Dispatch closure was not the one seen by the capability")
	}
}
