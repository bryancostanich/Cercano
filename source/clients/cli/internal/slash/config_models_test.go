package slash

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// TestFormatConfig_ModelTiers pins that /config show renders the model
// taxonomy: default provider plus each configured tier slot, sorted.
func TestFormatConfig_ModelTiers(t *testing.T) {
	cfg := &agentclient.Config{
		ModelsDefaultProvider: "open",
		ModelTiers: map[string]string{
			"fast_light_text.open": "phi4:14b",
			"everyday.cloud":       "claude-fable-5",
		},
	}
	out := formatConfig(cfg)
	for _, want := range []string{
		"models",
		"default-provider: open",
		"everyday.cloud",
		"claude-fable-5",
		"fast_light_text.open",
		"phi4:14b",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatConfig missing %q in:\n%s", want, out)
		}
	}
	// Deterministic ordering: everyday.cloud sorts before fast_light_text.open.
	if strings.Index(out, "everyday.cloud") > strings.Index(out, "fast_light_text.open") {
		t.Error("tier slots should render in sorted order")
	}
}
