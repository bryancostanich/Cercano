package mistralrs

import (
	"strings"
	"testing"
)

// TestRecommendedOpenModelsFillsChatTiers pins the wizard-facing contract: the
// recommendation fills every chat tier with a runtime-prefixed catalog id and
// deliberately omits embedding (mistral.rs doesn't serve embeddings; that tier
// stays on the shared cross-runtime model, added by the server).
func TestRecommendedOpenModelsFillsChatTiers(t *testing.T) {
	recs := RecommendedOpenModels(24 * 1024 * 1024 * 1024)
	if recs == nil {
		t.Fatal("RecommendedOpenModels returned nil (catalog failed to load)")
	}
	for _, tier := range []string{"most_capable", "everyday", "fast_light", "fast_light_text"} {
		id, ok := recs[tier]
		if !ok || id == "" {
			t.Errorf("tier %q missing from recommendations: %v", tier, recs)
			continue
		}
		if !strings.HasPrefix(id, runtimeName+":catalog:") {
			t.Errorf("tier %q id %q lacks the %q:catalog: prefix", tier, id, runtimeName)
		}
	}
	if _, ok := recs["embedding"]; ok {
		t.Errorf("mistral.rs recommendations must omit embedding, got %q", recs["embedding"])
	}
}

// TestProfileForRAM picks the largest profile at or below the machine's memory,
// and never nil for a below-smallest machine.
func TestProfileForRAM(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	prof, gb := cat.ProfileForRAM(8 * 1024 * 1024 * 1024) // below the smallest tier
	if prof == nil || gb == 0 {
		t.Fatalf("ProfileForRAM(8GB) = %v/%d, want the smallest profile", prof, gb)
	}
}
