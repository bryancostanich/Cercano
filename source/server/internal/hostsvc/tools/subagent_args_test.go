package tools

import (
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
)

// TestFormatSubagentLoopEvent_ToolUseStopCarriesArgs guards the fix for the
// child-tab "loading..." bug: a tool_use_stop LoopEvent carries its args in
// ArgsJSON (not Summary), and formatSubagentLoopEvent must forward that raw
// JSON so the runner can summarize it into the sub-agent tab's args line.
func TestBuildSubagentSystemPrompt_IsLeanAndBounded(t *testing.T) {
	prompt := buildSubagentSystemPrompt("/repo", []string{"Read", "Grep"})
	for _, want := range []string{
		"bounded Cercano sub-agent",
		"Working directory: /repo",
		"Use only the tools provided",
		"If the answer depends on repository contents",
		"- Read\n",
		"- Grep\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{
		"Delegate mechanical, well-specified work",
		"call the dispatch tool",
		"Before running git plumbing",
		"suggest_plan",
		"checkpoint tool",
		"planning mode",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt contains main-agent instruction %q:\n%s", forbidden, prompt)
		}
	}
}

func TestFormatSubagentLoopEvent_ToolUseStopCarriesArgs(t *testing.T) {
	ev := agent.LoopEvent{
		Kind:      agent.LoopToolUseStop,
		ToolUseID: "tid",
		ToolName:  "Bash",
		ArgsJSON:  `{"command":"git rev-parse HEAD"}`,
	}
	pe, ok := formatSubagentLoopEvent("sub1", "parent1", "sub 1", ev)
	if !ok {
		t.Fatal("expected the event to be forwarded")
	}
	if pe.Kind != "tool_use_stop" {
		t.Fatalf("Kind = %q, want tool_use_stop", pe.Kind)
	}
	if pe.ArgsJSON != ev.ArgsJSON {
		t.Fatalf("ArgsJSON = %q, want the raw tool args %q", pe.ArgsJSON, ev.ArgsJSON)
	}
}
