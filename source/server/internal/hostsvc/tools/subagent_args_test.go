package tools

import (
	"testing"

	"cercano/source/server/internal/agent"
)

// TestFormatSubagentLoopEvent_ToolUseStopCarriesArgs guards the fix for the
// child-tab "loading..." bug: a tool_use_stop LoopEvent carries its args in
// ArgsJSON (not Summary), and formatSubagentLoopEvent must forward that raw
// JSON so the runner can summarize it into the sub-agent tab's args line.
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
