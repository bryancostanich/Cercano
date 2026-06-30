package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/locus"
)

// TestAgenticDispatch_InvokesRunner verifies that Dispatch routes an Agentic
// spec to the installed AgenticRunner, passing through the resolved model and
// returning whatever the runner returns.
func TestAgenticDispatch_InvokesRunner(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })

	var gotSpec Spec
	var gotSel Selection
	var gotModel string
	eng.SetAgenticRunner(func(_ context.Context, spec Spec, sel Selection, model string) (Result, error) {
		gotSpec = spec
		gotSel = sel
		gotModel = model
		return Result{Text: "agentic done", Model: model, IsCloud: sel.IsCloud}, nil
	})

	res, err := eng.Dispatch(context.Background(), Spec{
		Mode:  Agentic,
		Role:  RoleCoproc,
		Task:  "summarise the codebase",
		Tools: []string{"Grep", "Read"},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Text != "agentic done" {
		t.Errorf("expected runner result, got %q", res.Text)
	}
	if gotModel != "local-model" {
		t.Errorf("expected model local-model, got %q", gotModel)
	}
	if gotSel.IsCloud {
		t.Errorf("local-only locus should not be cloud")
	}
	if gotSpec.Task != "summarise the codebase" {
		t.Errorf("spec.Task not forwarded: %q", gotSpec.Task)
	}
	if len(gotSpec.Tools) != 2 || gotSpec.Tools[0] != "Grep" {
		t.Errorf("spec.Tools not forwarded: %v", gotSpec.Tools)
	}
}

// TestAgenticDispatch_ModelOverride verifies ModelOverride applies to Agentic
// dispatches the same way it does for OneShot.
func TestAgenticDispatch_ModelOverride(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "default-model" })
	eng.SetAgenticRunner(func(_ context.Context, _ Spec, _ Selection, model string) (Result, error) {
		return Result{Text: "ok", Model: model}, nil
	})

	res, err := eng.Dispatch(context.Background(), Spec{
		Mode:          Agentic,
		Role:          RoleCoproc,
		Task:          "task",
		ModelOverride: "override-model",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Model != "override-model" {
		t.Errorf("expected override-model, got %q", res.Model)
	}
}

// TestAgenticDispatch_NoRunner returns a clear error when the runner is nil.
func TestAgenticDispatch_NoRunner(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })
	// no SetAgenticRunner call

	_, err := eng.Dispatch(context.Background(), Spec{Mode: Agentic, Role: RoleCoproc, Task: "x"})
	if err == nil {
		t.Fatal("expected error when runner is nil")
	}
	if !strings.Contains(err.Error(), "agentic runner not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestAgenticDispatch_RunnerError propagates runner errors back to the caller.
func TestAgenticDispatch_RunnerError(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })
	eng.SetAgenticRunner(func(_ context.Context, _ Spec, _ Selection, _ string) (Result, error) {
		return Result{}, errors.New("runner exploded")
	})

	_, err := eng.Dispatch(context.Background(), Spec{Mode: Agentic, Role: RoleCoproc, Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "runner exploded") {
		t.Errorf("expected runner error, got %v", err)
	}
}

// TestOneShotAgenticReturnsError is superseded by the tests above, but we
// keep the behaviour test: engine_test.go has one that now checks the
// "not configured" message path — here we just verify OneShot still works
// alongside Agentic without interference.
func TestAgenticAndOneShotCoexist(t *testing.T) {
	prov := echoProvider{}
	eng := NewEngine(
		provs(Providers{Local: prov}),
		func() locus.Mode { return locus.LocalOnly },
		nil,
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })
	eng.SetAgenticRunner(func(_ context.Context, _ Spec, _ Selection, _ string) (Result, error) {
		return Result{Text: "agentic"}, nil
	})

	// OneShot must still work.
	res, err := eng.Dispatch(context.Background(), Spec{
		Mode:   OneShot,
		Role:   RoleCoproc,
		Prompt: "ping",
	})
	if err != nil {
		t.Fatalf("OneShot Dispatch: %v", err)
	}
	if !strings.Contains(res.Text, "ping") {
		t.Errorf("expected echoed prompt in OneShot, got %q", res.Text)
	}

	// Agentic must also work.
	res2, err2 := eng.Dispatch(context.Background(), Spec{
		Mode: Agentic,
		Role: RoleCoproc,
		Task: "do stuff",
	})
	if err2 != nil {
		t.Fatalf("Agentic Dispatch: %v", err2)
	}
	if res2.Text != "agentic" {
		t.Errorf("expected agentic runner result, got %q", res2.Text)
	}
}
