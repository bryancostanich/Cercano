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
	return agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases(), builtins.CapabilitySynonyms())
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

func TestRunCommandCatalogAndLegacyGrant(t *testing.T) {
	reg := buildTestRegistry()
	for _, r := range []*agenttools.Registry{reg, reg.Subset([]string{"Bash"})} {
		found := false
		for _, tool := range agenttools.BuildToolCatalog(r) {
			if tool.Name == "Bash" {
				t.Fatal("legacy name advertised")
			}
			if tool.Name == "RunCommand" {
				found = true
			}
		}
		if !found {
			t.Fatal("RunCommand not advertised")
		}
		tool, ok := r.Get("Bash")
		if !ok || tool.Name() != "RunCommand" {
			t.Fatal("legacy lookup not resolved")
		}
	}
}

func TestLegacyAliasIsolationAndLifecycle(t *testing.T) {
	reg := buildTestRegistry()
	if _, ok := reg.Subset([]string{"Read"}).Get("Bash"); ok {
		t.Fatal("read-only grant leaked command tool")
	}
	if err := reg.RegisterAlias("Read", "RunCommand"); err == nil {
		t.Fatal("alias shadowed real tool")
	}
	if err := reg.RegisterAlias("Other", "Missing"); err == nil {
		t.Fatal("accepted missing target")
	}
	if err := reg.RegisterAlias("Bash", "Read"); err == nil {
		t.Fatal("alias overwritten")
	}
	reg.Unregister("Bash")
	if _, ok := reg.Get("RunCommand"); !ok {
		t.Fatal("alias removal removed primary")
	}
	if err := reg.RegisterAlias("Bash", "RunCommand"); err != nil {
		t.Fatal(err)
	}
	reg.Unregister("RunCommand")
	if _, ok := reg.Get("Bash"); ok {
		t.Fatal("dangling alias")
	}
	reg.MustRegister(agentadapter.AsTool(builtins.RunCommand(), "Bash", capabilities.Services{}))
}
