package slash

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// TestFormatConfig_ModelTiers pins that /config show renders the open override
// taxonomy: each configured runtime-tier override, sorted. (Cloud is not a tier
// slot.)
func TestFormatConfig_ModelTiers(t *testing.T) {
	cfg := &agentclient.Config{
		ModelTiers: map[string]string{
			"llama_server.fast_light_text": "phi4:14b",
			"llama_server.everyday":        "qwen3-coder",
		},
	}
	out := formatConfig(cfg)
	for _, want := range []string{
		"models",
		"llama_server.everyday",
		"qwen3-coder",
		"llama_server.fast_light_text",
		"phi4:14b",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatConfig missing %q in:\n%s", want, out)
		}
	}
	// Deterministic ordering: everyday sorts before fast_light_text.
	if strings.Index(out, "llama_server.everyday") > strings.Index(out, "llama_server.fast_light_text") {
		t.Error("tier slots should render in sorted order")
	}
}
