package server

import (
	"cercano/source/server/pkg/config"
)

// resolveTierModel resolves a taxonomy tier against the live config. It wraps
// the pure config.ModelsConfig.Resolve with one server-owned rule: an empty
// EVERYDAY slot falls through to the live legacy values (active cloud
// profile's model, open_model) so there is exactly one source of truth — no
// copy-based migration, no stale mirror. Other tiers resolve strictly from
// the models section; unconfigured means !ok and the caller decides.
//
// Callers must hold no cfgMu lock expectations — this takes the read lock.
func (s *Server) resolveTierModel(t config.Tier, prefer config.Provider, strict bool) (string, config.Provider, bool) {
	s.cfgMu.RLock()
	mc := s.currentConfig.Models
	s.cfgMu.RUnlock()

	if t == config.TierEveryday {
		if mc.Tiers.Everyday.Cloud == "" {
			mc.Tiers.Everyday.Cloud = s.activeCloudModel()
		}
	}
	return mc.Resolve(t, prefer, strict)
}

// watchdogModelFor returns the model override for watchdog one-shot checks.
// Explicit watchdog.model config wins. Otherwise the fast_light_text tier's
// OPEN side is used — strictly: the watchdog's oneShot lane dispatches to the
// local co-processor, so a cloud model id must never leak into it. Empty
// means the lane keeps its own default resolution.
func watchdogModelFor(wc config.WatchdogConfig, mc config.ModelsConfig) string {
	if wc.Model != "" {
		return wc.Model
	}
	if id, _, ok := mc.Resolve(config.TierFastLightText, config.ProviderOpen, true); ok {
		return id
	}
	return ""
}
