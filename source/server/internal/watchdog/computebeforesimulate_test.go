package watchdog

import (
	"context"
	"strings"
	"testing"
)

func TestComputeBeforeSimulateAppliesToBenchmarkAndSweep(t *testing.T) {
	ch := ComputeBeforeSimulateCheck()
	cases := []string{
		`{"cmd":["bash","-lc","go test -bench=. ./pkg/foo"],"cwd":"/repo"}`,
		`{"cmd":["python","scripts/parameter_sweep.py"],"cwd":"/repo"}`,
		`{"cmd":["ngspice","amp.cir"],"cwd":"/repo"}`,
	}
	for _, args := range cases {
		if !ch.Applies(Action{Kind: "tool_call", ToolName: "run_command", ToolArgs: []byte(args)}) {
			t.Fatalf("compute-before-simulate should apply to %s", args)
		}
	}
}

func TestComputeBeforeSimulateDoesNotApplyToOrdinaryTests(t *testing.T) {
	ch := ComputeBeforeSimulateCheck()
	a := Action{Kind: "tool_call", ToolName: "run_command", ToolArgs: []byte(`{"cmd":["bash","-lc","go test ./..."],"cwd":"/repo"}`)}
	if ch.Applies(a) {
		t.Fatal("ordinary tests should be governed by verification-strategy, not compute-before-simulate")
	}
}

func TestComputeBeforeSimulateChallengeNamesProtocol(t *testing.T) {
	ch := ComputeBeforeSimulateCheck()
	v, err := ch.Evaluate(context.Background(), Action{Kind: "tool_call", ToolName: "run_command", ToolArgs: []byte(`{"cmd":["hyperfine","./a.out"],"cwd":"/repo"}`)}, func(ctx context.Context, prompt string) (string, error) {
		if !strings.Contains(prompt, "compute-before-simulate protocol") {
			t.Fatalf("prompt should name the protocol: %s", prompt)
		}
		return "VIOLATION: yes\nCHALLENGE: model wording", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "compute-before-simulate" {
		t.Fatalf("verdict = %+v, want compute-before-simulate violation", v)
	}
	if !strings.Contains(v.Challenge, `get_protocol("compute-before-simulate")`) || !strings.Contains(v.Challenge, "comply") || !strings.Contains(v.Challenge, "justify") {
		t.Fatalf("challenge should tell the model to comply or justify with the protocol pull: %q", v.Challenge)
	}
}
