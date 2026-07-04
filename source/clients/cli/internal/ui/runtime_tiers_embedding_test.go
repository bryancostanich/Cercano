package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestTierRows_EmbeddingSlotReadsEmbeddingModel(t *testing.T) {
	rows := tierRows(&agentclient.Config{EmbeddingModel: "nomic-embed-text-v2-moe"})
	found := false
	for _, row := range rows {
		if row.Label == "embedding · open" {
			found = true
			if row.Value != "nomic-embed-text-v2-moe" {
				t.Errorf("embedding row value = %q", row.Value)
			}
			if row.Action.Kind != runtimeActionTierPick || row.Action.TierKey != "embedding.open" {
				t.Errorf("embedding row action = %+v", row.Action)
			}
		}
	}
	if !found {
		t.Fatal("no embedding · open row in tierRows")
	}
}
