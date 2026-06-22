package slash

import (
	"strings"
	"testing"
)

// /tools and /tool both rely on a live agentclient.Client for their server
// calls. We can't unit-test the live RPC paths here, but we CAN verify the
// command shapes — /tool dispatching to ResultInvokeTool, the no-arg usage
// hint, the help text being non-empty — without contacting the agent.

func TestRegisterTools_RegistersCommands(t *testing.T) {
	r := New()
	RegisterTools(r, nil)
	for _, want := range []string{"tools", "tool"} {
		if _, ok := r.cmds[want]; !ok {
			t.Errorf("missing command /%s", want)
		}
	}
}

func TestSlash_Tool_NoArgs_ShowsUsage(t *testing.T) {
	r := New()
	RegisterTools(r, nil)
	res, _ := r.Dispatch("/tool")
	if res.Kind != ResultText {
		t.Errorf("kind: got %v want ResultText", res.Kind)
	}
	if !strings.Contains(res.Text, "usage:") {
		t.Errorf("expected usage hint, got %q", res.Text)
	}
}

func TestSlash_Tool_DispatchesInvokeTool(t *testing.T) {
	r := New()
	RegisterTools(r, nil)
	res, _ := r.Dispatch(`/tool Grep {"pattern":"foo"}`)
	if res.Kind != ResultInvokeTool {
		t.Fatalf("kind: got %v want ResultInvokeTool", res.Kind)
	}
	if res.ToolName != "Grep" {
		t.Errorf("ToolName: got %q want Grep", res.ToolName)
	}
	if res.ToolArgs != `{"pattern":"foo"}` {
		t.Errorf("ToolArgs: got %q want {\"pattern\":\"foo\"}", res.ToolArgs)
	}
}

func TestSlash_Tool_DefaultsArgsToEmptyObject(t *testing.T) {
	r := New()
	RegisterTools(r, nil)
	res, _ := r.Dispatch("/tool git_status")
	if res.Kind != ResultInvokeTool {
		t.Fatalf("kind: got %v want ResultInvokeTool", res.Kind)
	}
	if res.ToolName != "git_status" {
		t.Errorf("name: got %q want git_status", res.ToolName)
	}
	if res.ToolArgs != "{}" {
		t.Errorf("args: expected default {}, got %q", res.ToolArgs)
	}
}

func TestSlash_Tool_HelpMentionsConfirmTiers(t *testing.T) {
	r := New()
	RegisterTools(r, nil)
	cmd, ok := r.Lookup("tool")
	if !ok {
		t.Fatal("expected /tool to be registered")
	}
	help := cmd.Help
	if !strings.Contains(help, "R-tier") || !strings.Contains(help, "W/X") {
		t.Errorf("/tool help should mention permission tiers; got: %q", help)
	}
}
