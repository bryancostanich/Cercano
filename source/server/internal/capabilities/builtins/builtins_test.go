package builtins

import (
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestRegister_Count(t *testing.T) {
	reg := capabilities.NewRegistry(capabilities.Services{})
	Register(reg)
	all := reg.All()
	if len(all) != 32 {
		names := make([]string, len(all))
		for i, c := range all {
			names[i] = c.Name()
		}
		t.Fatalf("expected 32 capabilities, got %d: %v", len(all), names)
	}
}

func TestWebCapabilities_AgentSurfaceOnly(t *testing.T) {
	// fetch, research, deep_research, and local must stay off the MCP
	// surface: the MCP server hand-registers legacy cercano_<name> handlers
	// for them, and the capability bridge would collide on those names.
	reg := capabilities.NewRegistry(capabilities.Services{})
	Register(reg)
	for _, name := range []string{"fetch", "research", "deep_research", "local"} {
		c, ok := reg.Get(name)
		if !ok {
			t.Errorf("capability %q not registered", name)
			continue
		}
		if !c.Surfaces().Has(capabilities.SurfaceAgent) {
			t.Errorf("%q must be exposed on the agent surface", name)
		}
		if c.Surfaces().Has(capabilities.SurfaceMCP) {
			t.Errorf("%q must NOT be on the MCP surface (legacy cercano_%s handler owns that name)", name, name)
		}
	}
}

func TestAgentAliases_Count(t *testing.T) {
	aliases := AgentAliases()
	want := 7
	if len(aliases) != want {
		t.Fatalf("expected %d aliases, got %d: %v", want, len(aliases), aliases)
	}
}

func TestAgentAliases_Entries(t *testing.T) {
	aliases := AgentAliases()
	expected := map[string]string{
		"read_file":   "Read",
		"list_dir":    "LS",
		"glob":        "Glob",
		"grep":        "Grep",
		"write_file":  "Write",
		"edit_file":   "Edit",
		"run_command": "Bash",
	}
	for k, v := range expected {
		got, ok := aliases[k]
		if !ok {
			t.Errorf("missing alias for %q", k)
			continue
		}
		if got != v {
			t.Errorf("alias[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestAgentAliases_ExcludesSynonyms(t *testing.T) {
	// dispatch <-> workflow lives in CapabilitySynonyms (both names visible),
	// not in AgentAliases (which would rename it and hide the canonical).
	aliases := AgentAliases()
	if _, ok := aliases["dispatch"]; ok {
		t.Errorf("dispatch must not be in AgentAliases; it belongs in CapabilitySynonyms")
	}
}

func TestCapabilitySynonyms_DispatchWorkflow(t *testing.T) {
	syns := CapabilitySynonyms()
	got, ok := syns["dispatch"]
	if !ok {
		t.Fatalf("expected synonyms for %q, got none: %v", "dispatch", syns)
	}
	if len(got) != 1 || got[0] != "workflow" {
		t.Errorf("dispatch synonyms = %v, want [workflow]", got)
	}
}
