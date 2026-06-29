package mcpadapter_test

import (
	"testing"
	"cercano/source/server/internal/capabilities"
	_ "cercano/source/server/internal/capabilities/builtins"
)

func TestMCPCatalogNonEmpty(t *testing.T) {
	catalog := capabilities.MCPCatalog()
	if len(catalog) == 0 {
		t.Fatal("MCPCatalog() returned empty — builtins init may not have run")
	}
	t.Logf("MCPCatalog len=%d", len(catalog))
	for _, m := range catalog {
		if m.Name == "" {
			t.Errorf("capability has empty name")
		}
		if len(m.Schema) == 0 {
			t.Errorf("capability %q has empty schema", m.Name)
		}
		t.Logf("  %s", m.Name)
	}
}
