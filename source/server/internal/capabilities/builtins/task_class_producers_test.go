package builtins

import (
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/modelbudget"
	"cercano/source/server/pkg/config"
	"context"
	"testing"
)

func TestTaskClassExplicitDispatch(t *testing.T) {
	for _, d := range config.TaskDefinitions() {
		svc, got := fakeDispatch(t, "ok")
		_, err := Dispatch().Execute(context.Background(), callWith(t, svc, map[string]any{"task": "fixture", "class": string(d.Task)}))
		if err != nil || got.RoutingTask != d.Task || got.Tier != "" {
			t.Errorf("class=%s got %+v err=%v", d.Task, got, err)
		}
	}
	svc, got := fakeDispatch(t, "ok")
	_, err := Dispatch().Execute(context.Background(), callWith(t, svc, map[string]any{"task": "fixture", "class": "unknown"}))
	if err == nil || got.RoutingTask != "" {
		t.Fatal("unknown class reached dispatch")
	}
}
func TestTaskClassReview(t *testing.T) {
	for _, tools := range [][]string{nil, {"Read"}} {
		svc, got := fakeDispatch(t, "VERDICT: HOLDS")
		_, err := Review().Execute(context.Background(), callWith(t, svc, map[string]any{"claim": "fixture", "tools": tools}))
		if err != nil || got.RoutingTask != config.TaskReview || got.Tier != "" {
			t.Fatalf("review=%+v err=%v", got, err)
		}
	}
}
func TestTaskClassResearchBudgetExecution(t *testing.T) {
	var specs []dispatch.Spec
	call := &capabilities.Call{Svc: capabilities.Services{
		DispatchTarget: func(_ context.Context, s dispatch.Spec) (modelbudget.Target, error) {
			specs = append(specs, s)
			return modelbudget.Target{ContextWindow: 32000, ContextWindowKnown: true}, nil
		},
		Dispatch: func(_ context.Context, s dispatch.Spec) (dispatch.Result, error) {
			specs = append(specs, s)
			return dispatch.Result{}, nil
		},
	}}
	m := &dispatchModelCaller{call: call, source: "research"}
	if _, err := m.Budget(context.Background(), 1024); err != nil {
		t.Fatal(err)
	}
	m.Call(context.Background(), "fixture")
	m.CallQuery(context.Background(), "query")
	for _, s := range specs {
		if s.RoutingTask != config.TaskResearch || s.Tier != "" {
			t.Fatalf("research=%+v", s)
		}
	}
	if len(specs) != 3 || !specs[2].DisableThinking || specs[1].DisableThinking {
		t.Fatal("query thinking changed")
	}
}
