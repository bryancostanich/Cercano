package agenttools

import (
	"testing"

	"cercano/source/server/internal/llm"
)

func TestBuildToolCatalog_CoversAllRegistered(t *testing.T) {
	reg := DefaultRegistry()
	cat := BuildToolCatalog(reg)
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
	reg := DefaultRegistry()
	cat := BuildToolCatalog(reg)
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
