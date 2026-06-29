package builtins

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestRunCoprocForwardsPromptAndWorkDir(t *testing.T) {
	var gotPrompt, gotDir string
	call := &capabilities.Call{
		WorkDir: "/proj",
		Svc: capabilities.Services{
			RunCoproc: func(_ context.Context, prompt, projectDir string) (string, error) {
				gotPrompt, gotDir = prompt, projectDir
				return "RESULT", nil
			},
		},
	}
	res, err := runCoproc(context.Background(), call, "do the thing")
	if err != nil {
		t.Fatal(err)
	}
	if gotPrompt != "do the thing" || gotDir != "/proj" {
		t.Fatalf("forwarded wrong: prompt=%q dir=%q", gotPrompt, gotDir)
	}
	if !strings.Contains(res.Text, "RESULT") {
		t.Fatalf("bad result text: %q", res.Text)
	}
}

func TestRunCoprocNilEngineErrors(t *testing.T) {
	_, err := runCoproc(context.Background(), &capabilities.Call{Svc: capabilities.Services{}}, "x")
	if err == nil {
		t.Fatal("expected error when RunCoproc is nil")
	}
}
