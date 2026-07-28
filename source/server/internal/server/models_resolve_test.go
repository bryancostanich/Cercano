package server

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// TestResolveTierModel_OpenIsTierOnly pins the open-tier resolver: it reads
// ONLY the tier's open slot. The retired open_model field must not resolve,
// and cloud is not the resolver's concern (cloud flows through
// DispatchModelFor's vendor-keyed path — see TestDispatchModelFor).
func TestResolveTierModel_OpenIsTierOnly(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.OpenModel = "qwen3-coder" // retired field: must be IGNORED
		c.ActiveCloudProfile = "messages-one"
	})

	// Empty everyday open slot: the retired open_model field must not resolve.
	if id, ok := s.resolveTierModel(config.TierEveryday); ok {
		t.Errorf("everyday open resolved (%q) from the retired open_model field", id)
	}

	// An explicitly configured everyday slot resolves.
	s.cfgSvc.Mutate(func(c *config.Config) { c.Models.Tiers.Everyday.Open = "qwen3-coder-next" })
	id, ok := s.resolveTierModel(config.TierEveryday)
	if !ok || id != "qwen3-coder-next" {
		t.Errorf("explicit everyday open = (%q,%v), want qwen3-coder-next", id, ok)
	}
}

// TestResolveTierModel_OtherTiersNoImplicitFallback pins that only the
// everyday tier borrows the legacy live values — an unconfigured fast tier is
// !ok, so a background helper can skip rather than silently running an 80B
// coder model as its "fast" lane.
func TestResolveTierModel_OtherTiersNoImplicitFallback(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Mutate(func(c *config.Config) { c.OpenModel = "qwen3-coder" })

	if id, ok := s.resolveTierModel(config.TierFastLightText); ok {
		t.Errorf("unconfigured fast_light_text must be !ok, got %q", id)
	}
}

// TestWatchdogModelFor pins the watchdog's model resolution: explicit
// watchdog.model config wins; else the fast_light_text tier's OPEN side
// (strict — the coproc dispatch lane runs local); else empty (the lane's
// own default resolution).
func TestWatchdogModelFor(t *testing.T) {
	wc := config.WatchdogConfig{Model: "override-model"}
	var mc config.ModelsConfig
	if got := watchdogModelFor(wc, mc); got != "override-model" {
		t.Errorf("explicit override: got %q", got)
	}

	wc.Model = ""
	mc.Tiers.FastLightText.Open = "phi4:14b"
	if got := watchdogModelFor(wc, mc); got != "phi4:14b" {
		t.Errorf("fast_light_text open: got %q, want phi4:14b", got)
	}

	// An empty open slot yields empty — the local coproc lane keeps its own
	// default resolution and no cloud model can leak in (cloud is never
	// resolved through the tier's open slot).
	mc.Tiers.FastLightText.Open = ""
	if got := watchdogModelFor(wc, mc); got != "" {
		t.Errorf("empty open tier: got %q, want empty", got)
	}
}

// TestUpdateConfig_ModelTier pins the /config write path: a model_tier_key
// patch updates the taxonomy, broadcasts the change, rebuilds the watchdog
// (its one-shot model is resolved at build time), and reports via GetConfig.
func TestUpdateConfig_ModelTier(t *testing.T) {
	s, _ := newTestServer()
	s.events = newEventHub()
	s.cfgSvc.Mutate(func(c *config.Config) { c.Watchdog.Enabled = true })
	s.watchdog = s.buildWatchdog()
	oldWatchdog := s.watchdog
	ch, unsub := s.events.subscribe()
	defer unsub()

	resp, err := s.UpdateConfig(t.Context(), &proto.UpdateConfigRequest{
		ModelTierKey: "fast_light_text.open", ModelTierValue: "phi4:14b",
	})
	if err != nil || !resp.Success {
		t.Fatalf("UpdateConfig: err=%v resp=%+v", err, resp)
	}
	if got := s.cfgSvc.Get().Models.Tiers.FastLightText.Open; got != "phi4:14b" {
		t.Errorf("tier slot = %q, want phi4:14b", got)
	}
	if s.watchdog == oldWatchdog {
		t.Error("watchdog should be rebuilt when a model tier changes (one-shot model is bound at build time)")
	}
	select {
	case ev := <-ch:
		cc := ev.GetConfigChanged()
		if cc == nil || cc.Field != "models.fast_light_text.open" || cc.Value != "phi4:14b" {
			t.Errorf("broadcast = %+v, want models.fast_light_text.open/phi4:14b", cc)
		}
	default:
		t.Error("expected a ConfigChanged broadcast for the tier patch")
	}

	gc, err := s.GetConfig(t.Context(), &proto.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if gc.ModelTiers["fast_light_text.open"] != "phi4:14b" {
		t.Errorf("GetConfig.ModelTiers = %v", gc.ModelTiers)
	}
}

// TestUpdateConfig_ModelTierInvalid pins that a bad key fails loudly without
// mutating anything.
func TestUpdateConfig_ModelTierInvalid(t *testing.T) {
	s, _ := newTestServer()

	resp, err := s.UpdateConfig(t.Context(), &proto.UpdateConfigRequest{
		ModelTierKey: "medium_rare.open", ModelTierValue: "x",
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

// TestDispatchModelFor pins the dispatch engine's model hook: strict on the
// requested tier's side, everyday fall-through when the slot is empty, and
// live config reads (no startup capture).
func TestDispatchModelFor(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.Models.Tiers.FastLightText.Open = "phi4:14b"
		c.Models.Tiers.Everyday.Open = "qwen3-coder-next"
	})

	if got := s.DispatchModelFor(false, config.TierFastLightText); got != "phi4:14b" {
		t.Errorf("fast_light_text open = %q, want phi4:14b", got)
	}
	// Empty slot falls through to everyday on the SAME side.
	if got := s.DispatchModelFor(false, config.TierFastLight); got != "qwen3-coder-next" {
		t.Errorf("empty fast_light open = %q, want everyday fall-through qwen3-coder-next", got)
	}
	// Live read: a runtime tier change is honored immediately.
	s.cfgSvc.Mutate(func(c *config.Config) { c.Models.Tiers.FastLightText.Open = "phi4-mini" })
	if got := s.DispatchModelFor(false, config.TierFastLightText); got != "phi4-mini" {
		t.Errorf("live read = %q, want phi4-mini", got)
	}
	// Cloud side: everyday cloud falls through to the active profile model.
	if got := s.DispatchModelFor(true, config.TierEveryday); got != s.activeCloudModel() {
		t.Errorf("everyday cloud = %q, want active profile model %q", got, s.activeCloudModel())
	}
}
