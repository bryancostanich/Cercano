package server

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// TestResolveTierModel_EverydayFallsThroughToLiveConfig pins the no-stale-
// mirror design: an empty everyday slot resolves to the LIVE values (active
// cloud profile's model / open_model) at resolution time, rather than a
// copied-at-migration mirror that can drift.
// TestResolveTierModel_EverydayCloudFallsThrough_OpenIsTierOnly: the cloud
// side of everyday still falls through to the live active-profile model,
// but the open side is TIER-ONLY — the legacy open_model field is retired
// and must not resolve (local-model-taxonomy design).
func TestResolveTierModel_EverydayCloudFallsThrough_OpenIsTierOnly(t *testing.T) {
	s, _ := newTestServer()
	s.currentConfig.OpenModel = "qwen3-coder" // retired field: must be IGNORED
	s.currentConfig.ActiveCloudProfile = "messages-one"

	id, prov, ok := s.resolveTierModel(config.TierEveryday, config.ProviderCloud, false)
	if !ok || prov != config.ProviderCloud {
		t.Fatalf("everyday cloud: (%q,%q,%v)", id, prov, ok)
	}
	if id != s.activeCloudModel() {
		t.Errorf("everyday cloud = %q, want live active-profile model %q", id, s.activeCloudModel())
	}

	// Open side, empty tier slot: the retired open_model field must not
	// resolve; non-strict lookup falls through to the cloud side instead.
	id, prov, ok = s.resolveTierModel(config.TierEveryday, config.ProviderOpen, false)
	if ok && prov == config.ProviderOpen {
		t.Errorf("everyday open resolved (%q) from the retired open_model field", id)
	}

	// An explicitly configured everyday slot resolves.
	s.currentConfig.Models.Tiers.Everyday.Open = "qwen3-coder-next"
	id, _, ok = s.resolveTierModel(config.TierEveryday, config.ProviderOpen, false)
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
	s.currentConfig.OpenModel = "qwen3-coder"

	if id, _, ok := s.resolveTierModel(config.TierFastLightText, config.ProviderOpen, true); ok {
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
	mc.Tiers.FastLightText.Cloud = "claude-haiku-4-5-20251001"
	if got := watchdogModelFor(wc, mc); got != "phi4:14b" {
		t.Errorf("fast_light_text open: got %q, want phi4:14b", got)
	}

	// Cloud-only tier config must NOT leak a cloud model into the local
	// coproc lane — empty means the lane keeps its own default.
	mc.Tiers.FastLightText.Open = ""
	if got := watchdogModelFor(wc, mc); got != "" {
		t.Errorf("cloud-only tier: got %q, want empty (no cross-provider leak)", got)
	}
}

// TestUpdateConfig_ModelTier pins the /config write path: a model_tier_key
// patch updates the taxonomy, broadcasts the change, rebuilds the watchdog
// (its one-shot model is resolved at build time), and reports via GetConfig.
func TestUpdateConfig_ModelTier(t *testing.T) {
	s, _ := newTestServer()
	s.events = newEventHub()
	s.currentConfig.Watchdog.Enabled = true
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
	if got := s.currentConfig.Models.Tiers.FastLightText.Open; got != "phi4:14b" {
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
