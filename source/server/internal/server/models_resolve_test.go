package server

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

func TestResolveTierModel_OverrideBeatsCatalogDefault(t *testing.T) {
	s, _ := newTestServer()

	catalogDefault := s.resolveTierModel(config.TierEveryday)
	if catalogDefault == "" {
		t.Fatal("expected a catalog default for everyday")
	}

	s.cfgSvc.Mutate(func(c *config.Config) {
		c.OpenModel = "ignored-legacy-field" // retired field: must be ignored
		c.Models.SetOverride(c.OpenRuntime, config.TierEveryday, "qwen3-coder-next")
	})
	if got := s.resolveTierModel(config.TierEveryday); got != "qwen3-coder-next" {
		t.Errorf("override everyday = %q, want qwen3-coder-next", got)
	}
}

func TestResolveTierModel_CatalogFallback(t *testing.T) {
	s, _ := newTestServer()
	if got := s.resolveTierModel(config.TierFastLightText); got == "" {
		t.Error("unconfigured fast_light_text should resolve from the catalog default")
	}
}

func TestWatchdogModelFor(t *testing.T) {
	s, _ := newTestServer()
	wc := config.WatchdogConfig{Model: "override-model"}
	if got := s.watchdogModelFor(wc); got != "override-model" {
		t.Errorf("explicit override: got %q", got)
	}

	wc.Model = ""
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.Models.SetOverride(c.OpenRuntime, config.TierFastLightText, "phi4:14b")
	})
	if got := s.watchdogModelFor(wc); got != "phi4:14b" {
		t.Errorf("fast_light_text override: got %q, want phi4:14b", got)
	}

	s.cfgSvc.Mutate(func(c *config.Config) {
		c.Models.SetOverride(c.OpenRuntime, config.TierFastLightText, "")
	})
	if got := s.watchdogModelFor(wc); got == "" {
		t.Error("empty override should fall back to the catalog default")
	}
}

func TestUpdateConfig_ModelTier(t *testing.T) {
	s, _ := newTestServer()
	s.events = newEventHub()
	s.cfgSvc.Mutate(func(c *config.Config) { c.Watchdog.Enabled = true })
	s.watchdog = s.buildWatchdog()
	oldWatchdog := s.watchdog
	ch, unsub := s.events.subscribe()
	defer unsub()

	resp, err := s.UpdateConfig(t.Context(), &proto.UpdateConfigRequest{
		ModelTierKey: "llama_server.fast_light_text", ModelTierValue: "phi4:14b",
	})
	if err != nil || !resp.Success {
		t.Fatalf("UpdateConfig: err=%v resp=%+v", err, resp)
	}
	got, ok := s.cfgSvc.Get().Models.OverrideFor("llama_server", config.TierFastLightText)
	if !ok || got != "phi4:14b" {
		t.Errorf("tier override = (%q,%v), want phi4:14b,true", got, ok)
	}
	if s.watchdog == oldWatchdog {
		t.Error("watchdog should be rebuilt when a model tier changes (one-shot model is bound at build time)")
	}
	select {
	case ev := <-ch:
		cc := ev.GetConfigChanged()
		if cc == nil || cc.Field != "models.llama_server.fast_light_text" || cc.Value != "phi4:14b" {
			t.Errorf("broadcast = %+v, want models.llama_server.fast_light_text/phi4:14b", cc)
		}
	default:
		t.Error("expected a ConfigChanged broadcast for the tier patch")
	}

	gc, err := s.GetConfig(t.Context(), &proto.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if gc.ModelTiers["llama_server.fast_light_text"] != "phi4:14b" {
		t.Errorf("GetConfig.ModelTiers = %v", gc.ModelTiers)
	}
}

func TestUpdateConfig_ModelTierInvalid(t *testing.T) {
	s, _ := newTestServer()

	resp, err := s.UpdateConfig(t.Context(), &proto.UpdateConfigRequest{
		ModelTierKey: "llama_server.medium_rare", ModelTierValue: "x",
	})
	if err != nil {
		t.Fatalf("UpdateConfig transport err: %v", err)
	}
	if resp.Success {
		t.Error("invalid tier key must fail")
	}
	if !strings.Contains(resp.Message, "medium_rare") {
		t.Errorf("error should name the bad tier, got %q", resp.Message)
	}
}

func TestDispatchModelFor(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.Models.SetOverride(c.OpenRuntime, config.TierFastLightText, "phi4:14b")
		c.Models.SetOverride(c.OpenRuntime, config.TierEveryday, "qwen3-coder-next")
	})

	if got := s.DispatchModelFor(false, config.TierFastLightText); got != "phi4:14b" {
		t.Errorf("fast_light_text open = %q, want phi4:14b", got)
	}
	// Empty fast_light falls through to its catalog default, not the everyday
	// override. DispatchModelFor only falls back to everyday when a tier cannot
	// resolve at all.
	if got := s.DispatchModelFor(false, config.TierFastLight); got == "" || got == "qwen3-coder-next" {
		t.Errorf("fast_light open = %q, want a non-empty catalog default distinct from everyday override", got)
	}
	// Live read: a runtime tier change is honored immediately.
	s.cfgSvc.Mutate(func(c *config.Config) { c.Models.SetOverride(c.OpenRuntime, config.TierFastLightText, "phi4-mini") })
	if got := s.DispatchModelFor(false, config.TierFastLightText); got != "phi4-mini" {
		t.Errorf("live read = %q, want phi4-mini", got)
	}
	// Cloud side: everyday cloud resolves through the active profile model.
	if got := s.DispatchModelFor(true, config.TierEveryday); got != s.activeCloudModel() {
		t.Errorf("everyday cloud = %q, want active profile model %q", got, s.activeCloudModel())
	}
}
