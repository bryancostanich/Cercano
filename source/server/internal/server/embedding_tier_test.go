package server

import (
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// The embedding.open tier slot is UI over the embedding_model config
// field — it must write that field, not a Models-taxonomy entry.
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
	if got := s.cfgSvc.Get().Models.Tiers.Embedding.Open; got != "nomic-embed-text-v2-moe" {
		t.Errorf("embedding.open tier slot = %q", got)
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
	s.cfgSvc.Mutate(func(c *config.Config) { c.Models.Tiers.Embedding.Open = "old-model" })
	resp, err := s.UpdateConfig(t.Context(), &proto.UpdateConfigRequest{
		ModelTierKey: "embedding.open", ModelTierValue: "-",
	})
	if err != nil || !resp.Success {
		t.Fatalf("UpdateConfig: err=%v resp=%+v", err, resp)
	}
	if got := s.cfgSvc.Get().Models.Tiers.Embedding.Open; got != "" {
		t.Errorf("embedding.open tier slot = %q, want cleared", got)
	}
}
