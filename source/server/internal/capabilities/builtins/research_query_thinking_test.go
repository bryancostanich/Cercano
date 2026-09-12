package builtins

import (
	"context"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/modelbudget"
	"cercano/source/server/internal/web"
)

func TestResearchDisablesThinkingOnlyForQueryGeneration(t *testing.T) {
	var calls []dispatch.Spec
	caller := &dispatchModelCaller{call: &capabilities.Call{Svc: capabilities.Services{
		Dispatch: func(_ context.Context, s dispatch.Spec) (dispatch.Result, error) {
			calls = append(calls, s)
			return dispatch.Result{Text: "1. go embed documentation"}, nil
		},
		DispatchTarget: func(context.Context, dispatch.Spec) (modelbudget.Target, error) {
			return modelbudget.Target{Provider: "llama_server", Model: "model", ContextWindow: 131072, ContextWindowKnown: true}, nil
		},
	}}, source: "research"}
	pipeline := web.NewResearchPipeline(caller, nil, nil)
	if _, err := pipeline.CraftQueries(context.Background(), "What embeds files in Go?"); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Synthesize(context.Background(), "What embeds files?", []web.FetchedPage{{Title: "Embed", URL: "https://go.dev/doc", Content: "Use go:embed."}}); err != nil {
		t.Fatal(err)
	}
	if _, err := caller.Call(context.Background(), "Analyze a finding"); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.CraftQueries(context.Background(), "Another query"); err != nil {
		t.Fatal(err)
	}
	want := []bool{true, false, false, true}
	if len(calls) != len(want) {
		t.Fatalf("calls=%d", len(calls))
	}
	for i, s := range calls {
		if s.DisableThinking != want[i] {
			t.Errorf("call %d DisableThinking=%v want=%v", i, s.DisableThinking, want[i])
		}
	}
}
