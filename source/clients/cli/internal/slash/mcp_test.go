package slash

import "testing"

func TestParseMcpSubcommand(t *testing.T) {
	sub, rest := parseMcp([]string{"add", "github", "npx", "-y", "server-github"})
	if sub != "add" {
		t.Fatalf("sub = %q", sub)
	}
	if len(rest) != 4 || rest[0] != "github" || rest[1] != "npx" {
		t.Fatalf("rest = %v", rest)
	}
	// Bare /mcp defaults to list.
	sub, _ = parseMcp(nil)
	if sub != "list" {
		t.Fatalf("default sub = %q", sub)
	}
}
