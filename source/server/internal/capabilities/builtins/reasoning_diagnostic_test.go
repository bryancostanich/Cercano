package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/reasoningexperiment"
)

func TestReasoningDiagnosticCapability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "experiment.json"), []byte(`{"profile":"selected"}`), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc := capabilities.Services{ReasoningDiagnostic: func(_ context.Context, s reasoningexperiment.Spec) (reasoningexperiment.Report, error) {
		calls++
		if s.Profile != "selected" {
			t.Fatal("wrong input")
		}
		return reasoningexperiment.Report{Profile: s.Profile}, nil
	}}
	c := ReasoningDiagnostic()
	if c.Tier() != capabilities.TierX || c.Surfaces() != capabilities.SurfaceAgent {
		t.Fatal("wrong permission/surface")
	}
	call := &capabilities.Call{WorkDir: dir, Args: json.RawMessage(`{"input_path":"experiment.json"}`), Svc: svc}
	r, e := c.Execute(t.Context(), call)
	if e != nil || r.Type != capabilities.ResultJSON || calls != 1 {
		t.Fatalf("%+v %v", r, e)
	}
	for _, args := range []string{`{}`, `{"input_path":"experiment.json","key":"not allowed"}`, `{"input_path":"missing"}`, `{"input_path":"."}`} {
		call.Args = json.RawMessage(args)
		if _, e = c.Execute(t.Context(), call); e == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid input reached provider")
	}
	reg := capabilities.NewRegistry(svc)
	Register(reg)
	if _, ok := reg.Get("reasoning_diagnostic"); !ok {
		t.Fatal("tool not installed")
	}
}
