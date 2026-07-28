package slash

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// TestFormatConfig_ModelTiers pins that /config show renders the open model
// taxonomy: each configured open tier slot, sorted. (Cloud is not a tier slot.)
func TestFormatConfig_ModelTiers(t *testing.T) {
	cfg := &agentclient.Config{
		ModelTiers: map[string]string{
			"fast_light_text.open": "phi4:14b",
			"everyday.open":        "qwen3-coder",
		},
	}
	out := formatConfig(cfg)
	for _, want := range []string{
		"models",
		"everyday.open",
		"qwen3-coder",
		"fast_light_text.open",
		"phi4:14b",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatConfig missing %q in:\n%s", want, out)
		}
	}
	// Deterministic ordering: everyday.open sorts before fast_light_text.open.
	if strings.Index(out, "everyday.open") > strings.Index(out, "fast_light_text.open") {
		t.Error("tier slots should render in sorted order")
	}
}
