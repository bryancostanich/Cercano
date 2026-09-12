package agent

import (
	"testing"

	"cercano/source/server/internal/agenttools"
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

// Regression: a sub-agent on a small local model must not reserve a
// cloud-sized output budget. Observed in the field on glm-4.5-air, where an
// 8192 reserve inside a 16384 window left too little room and every dispatch
// died in preflight with context_overflow before sending a token.
func TestToolLoop_SmallWindowClampsOutputReserve(t *testing.T) {
	for _, tc := range []struct {
		name          string
		window        int
		bareRegistry  bool
		wantMaxTokens int
	}{
		{"local 16k clamps to quarter window", 16384, false, 4096},
		// Uses a bare registry: the full tool catalog alone exceeds a 2K window,
		// so only an empty catalog isolates the reserve floor from schema size.
		{"tiny window keeps usable floor", 2048, true, 1024},
		{"cloud window keeps full default", 200000, false, config.DefaultToolLoopMaxTokensPerTurn},
		{"unresolved window keeps default", 0, false, config.DefaultToolLoopMaxTokensPerTurn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := &mockProvider{
				scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hi"}}},
				caps:    inference.Capabilities{SupportsTools: true},
			}
			perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
			reg := testDefaultRegistry()
			if tc.bareRegistry {
				reg = agenttools.NewRegistry()
			}

			if _, err := RunToolLoop(t.Context(), ToolLoopInput{
				Provider: prov, Registry: reg, Permissions: perms,
				UserInput: "x", ContextWindow: tc.window, ContextWindowKnown: tc.window > 0,
			}); err != nil {
				t.Fatal(err)
			}
			if len(prov.reqs) == 0 {
				t.Fatal("no request recorded")
			}
			if got := prov.reqs[0].MaxTokens; got != tc.wantMaxTokens {
				t.Errorf("MaxTokens = %d, want %d (window=%d)", got, tc.wantMaxTokens, tc.window)
			}
		})
	}
}

// The clamp must not smuggle in tight-context catalog restriction: a sub-agent
// granted a tool still sees it advertised on a small local window.
func TestToolLoop_SmallWindowKeepsGrantedToolsAdvertised(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hi"}}},
		caps:    inference.Capabilities{SupportsTools: true},
	}
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	if _, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: testDefaultRegistry(), Permissions: perms,
		UserInput: "x", ContextWindow: 16384, ContextWindowKnown: true, DebugMode: true, // include the full debug catalog for this clamp regression
	}); err != nil {
		t.Fatal(err)
	}
	full := len(agenttools.BuildToolCatalog(testDefaultRegistry()))
	if got := len(prov.reqs[0].Tools); got != full {
		t.Errorf("advertised %d tools, want full catalog %d; clamp must not restrict tools", got, full)
	}
}
