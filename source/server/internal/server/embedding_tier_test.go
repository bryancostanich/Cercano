package server

import (
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// The embedding.open compatibility key writes the active runtime's embedding
// override and broadcasts embedding_model for the UI/restart notice.
func TestUpdateConfig_EmbeddingTierWritesEmbeddingModel(t *testing.T) {
	s, _ := newTestServer()
	s.events = newEventHub()
	ch, unsub := s.events.subscribe()
	defer unsub()

	resp, err := s.UpdateConfig(t.Context(), &proto.UpdateConfigRequest{
		ModelTierKey: "embedding.open", ModelTierValue: "nomic-embed-text-v2-moe",
	})
	if err != nil || !resp.Success {
		t.Fatalf("UpdateConfig: err=%v resp=%+v", err, resp)
	}
	got, ok := s.cfgSvc.Get().Models.OverrideFor(s.cfgSvc.Get().OpenRuntime, config.TierEmbedding)
	if !ok || got != "nomic-embed-text-v2-moe" {
		t.Errorf("embedding override = (%q,%v)", got, ok)
	}
	select {
	case ev := <-ch:
		cc := ev.GetConfigChanged()
		if cc == nil || cc.Field != "embedding_model" || cc.Value != "nomic-embed-text-v2-moe" {
			t.Errorf("broadcast = %+v, want embedding_model change", cc)
		}
	default:
		t.Error("no ConfigChanged broadcast")
	}
}

func TestUpdateConfig_EmbeddingTierClear(t *testing.T) {
	s, _ := newTestServer()
	s.events = newEventHub()
	s.cfgSvc.Mutate(func(c *config.Config) { c.Models.SetOverride(c.OpenRuntime, config.TierEmbedding, "old-model") })
	resp, err := s.UpdateConfig(t.Context(), &proto.UpdateConfigRequest{
		ModelTierKey: "embedding.open", ModelTierValue: "-",
	})
	if err != nil || !resp.Success {
		t.Fatalf("UpdateConfig: err=%v resp=%+v", err, resp)
	}
	if got, ok := s.cfgSvc.Get().Models.OverrideFor(s.cfgSvc.Get().OpenRuntime, config.TierEmbedding); ok || got != "" {
		t.Errorf("embedding override = (%q,%v), want cleared", got, ok)
	}
}
