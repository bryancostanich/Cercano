package server

import (
	"testing"

	"cercano/source/server/pkg/config"
)

// TestResolveTierModel_EverydayFallsThroughToLiveConfig pins the no-stale-
// mirror design: an empty everyday slot resolves to the LIVE values (active
// cloud profile's model / open_model) at resolution time, rather than a
// copied-at-migration mirror that can drift.
func TestResolveTierModel_EverydayFallsThroughToLiveConfig(t *testing.T) {
	s, _ := newTestServer()
	s.currentConfig.OpenModel = "qwen3-coder"
	s.currentConfig.ActiveCloudProfile = "messages-one"

	id, prov, ok := s.resolveTierModel(config.TierEveryday, config.ProviderCloud, false)
	if !ok || prov != config.ProviderCloud {
		t.Fatalf("everyday cloud: (%q,%q,%v)", id, prov, ok)
	}
	if id != s.activeCloudModel() {
		t.Errorf("everyday cloud = %q, want live active-profile model %q", id, s.activeCloudModel())
	}

	id, prov, ok = s.resolveTierModel(config.TierEveryday, config.ProviderOpen, false)
	if !ok || prov != config.ProviderOpen || id != "qwen3-coder" {
		t.Errorf("everyday open = (%q,%q,%v), want live open_model qwen3-coder", id, prov, ok)
	}

	// An explicitly configured everyday slot wins over the live fallback.
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
