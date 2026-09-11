package worker

import (
	"cercano/source/server/pkg/config"
	"testing"
)

func TestRoutingContractLocalVisionSurvivesSnapshot(t *testing.T) {
	cfg := config.Config{OpenRuntime: "fixture"}
	slots := map[string]string{string(config.TierVision): "local-image", string(config.TierEveryday): "local-text"}
	got := ConfigFromSnapshot(SnapshotConfig(cfg, "", slots))
	text := openTierModel(got, config.TierEveryday)
	image := openTierModel(got, config.TierVision)
	t.Logf("round trip: runtime=%q text=%q image=%q", got.OpenRuntime, text, image)
	if text != "local-text" {
		t.Fatalf("control text slot lost: %q", text)
	}
	if image != "local-image" {
		t.Errorf("vision slot lost: got %q, want local-image", image)
	}
}
