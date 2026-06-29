package agenttools_test

import (
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/llm"
)

// buildTestRegistry constructs the agenttools.Registry the same way the server
// does at runtime, using an empty Services (no providers needed by builtins).
func buildTestRegistry() *agenttools.Registry {
	capReg := capabilities.NewRegistry(capabilities.Services{})
	builtins.Register(capReg)
	return agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases())
}

func TestBuildToolCatalog_CoversAllRegistered(t *testing.T) {
	reg := buildTestRegistry()
	cat := agenttools.BuildToolCatalog(reg)
	if len(cat) != len(reg.All()) {
		t.Errorf("catalog len %d != registry len %d", len(cat), len(reg.All()))
	}
	for _, tl := range cat {
		if tl.Name == "" || tl.Description == "" || len(tl.Schema) == 0 {
			t.Errorf("incomplete catalog entry: %+v", tl)
		}
		switch tl.Permission {
		case llm.PermR, llm.PermW, llm.PermX:
		default:
			t.Errorf("invalid permission tier: %+v", tl)
		}
	}
}

func TestBuildToolCatalog_PreservesPermissionTier(t *testing.T) {
	reg := buildTestRegistry()
	cat := agenttools.BuildToolCatalog(reg)
	byName := map[string]llm.Tool{}
	for _, tl := range cat {
		byName[tl.Name] = tl
	}
	if byName["rm_file"].Permission != llm.PermX {
		t.Errorf("rm_file should be X, got %v", byName["rm_file"].Permission)
	}
	if byName["Read"].Permission != llm.PermR {
		t.Errorf("Read should be R, got %v", byName["Read"].Permission)
	}
}
