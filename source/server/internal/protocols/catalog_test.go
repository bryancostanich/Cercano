package protocols

import (
	"strings"
	"testing"
)

func TestCoreCatalogComplete(t *testing.T) {
	want := []string{"compute-before-simulate", "design-decisions", "systematic-debugging", "verification-strategy"}
	for _, name := range want {
		p, ok := Get(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if p.Domain != DomainCore {
			t.Fatalf("%s should be core", name)
		}
		if len(strings.TrimSpace(p.Body)) < 200 {
			t.Fatalf("%s body looks too short to be the real protocol", name)
		}
		if !strings.HasSuffix(p.Trigger, ".") {
			t.Fatalf("%s trigger should be a full sentence", name)
		}
	}
}

func TestDesignDecisionsHasMergedSteps(t *testing.T) {
	p, _ := Get("design-decisions")
	if !strings.Contains(p.Body, "Symmetric quantification") && !strings.Contains(p.Body, "symmetric quantification") {
		t.Fatal("design-decisions missing the symmetric quantification rule")
	}
	if !strings.Contains(strings.ToLower(p.Body), "argue against your own recommendation") {
		t.Fatal("design-decisions missing the argue-against-yourself step")
	}
}
