package watchdog

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

type computeBeforeSimulateCheck struct{}

// ComputeBeforeSimulateCheck enforces doing the analytical expectation before
// simulations, benchmarks, and parameter sweeps.
func ComputeBeforeSimulateCheck() Check { return computeBeforeSimulateCheck{} }

func (computeBeforeSimulateCheck) Name() string { return "compute-before-simulate" }

func (computeBeforeSimulateCheck) Applies(a Action) bool {
	if a.Kind != "tool_call" || canonical(a.ToolName) != runCommandToolName {
		return false
	}
	line, ok := runCommandLine(a.ToolArgs)
	if !ok {
		return false
	}
	return looksLikeSimulationCommand(line)
}

func (computeBeforeSimulateCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "compute-before-simulate"}, nil // no model → fail open
	}
	out, err := oneShot(ctx, buildComputeBeforeSimulatePrompt(a))
	if err != nil {
		return Verdict{}, err
	}
	v := parseVerdict("compute-before-simulate", out)
	if v.Violation {
		v.Challenge = "You're about to run a simulation, benchmark, or parameter sweep without evidence of the compute-before-simulate protocol — comply by calling get_protocol(\"compute-before-simulate\") and writing the expected result/equation first, or justify why this run is not a simulation-style verification."
	}
	return v, nil
}

func buildComputeBeforeSimulatePrompt(a Action) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing the compute-before-simulate protocol.\n")
	b.WriteString("The agent is about to run a command that looks like a simulation, benchmark, parameter sweep, or performance/modeling run. Judge ONLY whether the transcript lacks an analytical expected result before the run: a specific expected quantity, the equation or reasoning that produced it, and how the run will verify it. Do NOT flag ordinary unit tests or a post-analysis rerun when the expected result is already present.\n\n")
	fmt.Fprintf(&b, "Proposed simulation-style action: %s %s\n\n", a.ToolName, string(a.ToolArgs))
	b.WriteString("Recent transcript:\n")
	b.WriteString(transcriptTail(a.Transcript, 18))
	b.WriteString("\n\nRespond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}

var sweepPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])(sweep|sweeps|benchmark|bench|simulate|simulation|simulator|spice|ngspice|iverilog|verilator|cocotb|pytest-benchmark|hyperfine|perf|ab|wrk|jmeter|loadtest|locust|parameter[\s_-]*sweep)([^a-z0-9]|$)`)

func looksLikeSimulationCommand(line string) bool {
	l := strings.ToLower(line)
	if strings.Contains(l, "go test") || strings.Contains(l, "pytest") || strings.Contains(l, "npm test") {
		// Unit/integration test commands are governed by verification-strategy
		// unless their arguments explicitly name a benchmark/simulation/sweep.
		return strings.Contains(l, "bench") || strings.Contains(l, "benchmark") || strings.Contains(l, "simulate") || strings.Contains(l, "sweep")
	}
	return sweepPattern.MatchString(l)
}
