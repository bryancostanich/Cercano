package builtins

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
)

func TestRunCoprocForwardsSpecFields(t *testing.T) {
	var gotSpec dispatch.Spec
	call := &capabilities.Call{
		WorkDir: "/proj",
		Svc: capabilities.Services{
			Dispatch: func(_ context.Context, spec dispatch.Spec) (dispatch.Result, error) {
				gotSpec = spec
				return dispatch.Result{Text: "RESULT"}, nil
			},
		},
	}
	res, err := runCoproc(context.Background(), call, "summarize", "do the thing", "input content")
	if err != nil {
		t.Fatal(err)
	}
	if gotSpec.Source != "summarize" {
		t.Errorf("Source = %q, want %q", gotSpec.Source, "summarize")
	}
	if gotSpec.Prompt != "do the thing" {
		t.Errorf("Prompt = %q, want %q", gotSpec.Prompt, "do the thing")
	}
	if gotSpec.WorkDir != "/proj" {
		t.Errorf("WorkDir = %q, want %q", gotSpec.WorkDir, "/proj")
	}
	if !gotSpec.WantsProjectContext {
		t.Error("WantsProjectContext = false, want true")
	}
	if !gotSpec.RecordUsage {
		t.Error("RecordUsage = false, want true")
	}
	if gotSpec.ContentTokensAvoided == 0 {
		t.Error("ContentTokensAvoided = 0, want non-zero")
	}
	if gotSpec.Mode != dispatch.OneShot {
		t.Errorf("Mode = %v, want OneShot", gotSpec.Mode)
	}
	if gotSpec.Role != dispatch.RoleCoproc {
		t.Errorf("Role = %v, want RoleCoproc", gotSpec.Role)
	}
	if !strings.Contains(res.Text, "RESULT") {
		t.Fatalf("bad result text: %q", res.Text)
	}
}

func TestRunCoprocNilDispatchErrors(t *testing.T) {
	_, err := runCoproc(context.Background(), &capabilities.Call{Svc: capabilities.Services{}}, "summarize", "x", "x")
	if err == nil {
		t.Fatal("expected error when Dispatch is nil")
	}
}
