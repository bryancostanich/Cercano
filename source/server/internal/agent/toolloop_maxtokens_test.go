package agent

import (
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

// When MaxTokensPerTurn is unset (0), the loop must request the config default
// output budget — not the old bare 4096 literal, which truncated real files.
func TestToolLoop_DefaultMaxTokensPerTurn(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hi"}}},
		caps:    inference.Capabilities{SupportsTools: true},
	}
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	if _, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: testDefaultRegistry(), Permissions: perms,
		UserInput: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if len(prov.reqs) == 0 {
		t.Fatal("no request recorded")
	}
	if got := prov.reqs[0].MaxTokens; got != config.DefaultToolLoopMaxTokensPerTurn {
		t.Errorf("MaxTokens = %d, want default %d", got, config.DefaultToolLoopMaxTokensPerTurn)
	}
}

// An explicit MaxTokensPerTurn overrides the default.
func TestToolLoop_MaxTokensPerTurnOverride(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hi"}}},
		caps:    inference.Capabilities{SupportsTools: true},
	}
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	if _, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: testDefaultRegistry(), Permissions: perms,
		UserInput: "x", MaxTokensPerTurn: 1234,
	}); err != nil {
		t.Fatal(err)
	}
	if got := prov.reqs[0].MaxTokens; got != 1234 {
		t.Errorf("MaxTokens = %d, want override 1234", got)
	}
}

func TestToolLoop_TightFallbackBoundsOutputForSmallContext(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hi"}}},
		caps:    inference.Capabilities{SupportsTools: true},
	}
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	if _, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: testDefaultRegistry(), Permissions: perms,
		UserInput: "x", ContextWindow: 16384, TightContextFallback: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := prov.reqs[0].MaxTokens, 4096; got != want {
		t.Errorf("MaxTokens = %d, want tight-fallback limit %d", got, want)
	}
}
