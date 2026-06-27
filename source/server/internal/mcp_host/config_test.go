package mcphost

import "testing"

func TestToolNameAndDisplay(t *testing.T) {
	fq := ToolName("github", "create_issue")
	if fq != "mcp__github__create_issue" {
		t.Fatalf("ToolName = %q", fq)
	}
	if got := DisplayName(fq); got != "mcp/github/create_issue" {
		t.Fatalf("DisplayName = %q", got)
	}
	// Non-mcp names pass through unchanged.
	if got := DisplayName("Read"); got != "Read" {
		t.Fatalf("DisplayName passthrough = %q", got)
	}
}
