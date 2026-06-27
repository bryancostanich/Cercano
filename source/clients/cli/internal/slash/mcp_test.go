package slash

import (
	"strings"
	"testing"
)

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

func TestRegisterMcp_RegistersCommand(t *testing.T) {
	r := New()
	RegisterMcp(r, nil)
	if _, ok := r.cmds["mcp"]; !ok {
		t.Fatal("missing command /mcp")
	}
}

// The list/add/remove/restart success paths contact a live agentclient.Client,
// so (matching /tools' test convention) we exercise only the arg-validation and
// unknown-subcommand branches, which return usage text without touching the
// client. A nil client is safe on these paths.
func TestSlash_Mcp_UsagePaths(t *testing.T) {
	r := New()
	RegisterMcp(r, nil)
	cases := map[string]string{
		"/mcp add github":  "usage: /mcp add",     // <2 args after sub
		"/mcp remove":      "usage: /mcp remove",  // missing name
		"/mcp restart":     "usage: /mcp restart", // missing name
		"/mcp bogusaction": "usage: /mcp",         // unknown sub → general usage
	}
	for line, want := range cases {
		res, _ := r.Dispatch(line)
		if res.Kind != ResultText {
			t.Errorf("%q: kind = %v, want ResultText", line, res.Kind)
		}
		if !strings.Contains(res.Text, want) {
			t.Errorf("%q: got %q, want substring %q", line, res.Text, want)
		}
	}
}
