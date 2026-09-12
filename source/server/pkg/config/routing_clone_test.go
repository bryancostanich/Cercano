package config

import "testing"

func TestRoutingSnapshotLocalModelsAreIndependent(t *testing.T) {
	c := Config{}
	c.Models.SetOverride("ollama", TierEveryday, "saved")
	clone := c.Clone()
	clone.Models.SetOverride("ollama", TierEveryday, "new")
	clone.Models.SetOverride("llama_server", TierMostCapable, "other-runtime")
	if model, _ := c.Models.OverrideFor("ollama", TierEveryday); model != "saved" {
		t.Fatalf("snapshot aliased local model map: %q", model)
	}
	if _, ok := c.Models.OverrideFor("llama_server", TierMostCapable); ok {
		t.Fatal("snapshot aliased outer runtime map")
	}
}
