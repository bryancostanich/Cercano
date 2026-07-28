package server

import (
	"cercano/source/server/pkg/config"
)

// resolveTierModel resolves a tier's EFFECTIVE open model on the active runtime
// (override-else-catalog-default) via the single resolver. Empty means neither
// an override nor a catalog default exists for the tier.
func (s *Server) resolveTierModel(t config.Tier) string {
	return s.openModels.Model(t)
}

// DispatchModelFor resolves the model id for a dispatch: the provider side is
// already chosen by locus, the tier names the capability class.
//
// Cloud: resolution runs through the active profile's vendor cost table
// (model_profiles.cloud) — the capability tier maps to a cost tier, and the
// active vendor owns the concrete model. When the vendor+tier slot is unset it
// falls back to the active profile's own Model, which is vendor-correct by
// construction. This is what stops a foreign-vendor id (an Anthropic model on a
// Codex-routed profile) from ever being dispatched.
//
// Open: unchanged — strict on the requested tier's open slot, falling through
// to the everyday open workhorse so a sparse tier table never yields empty.
//
// Reads live config, so a runtime tier or profile change is honored on the very
// next dispatch.
func (s *Server) DispatchModelFor(isCloud bool, tier config.Tier) string {
	if isCloud {
		prof, ok := s.activeProfile()
		if !ok {
			return ""
		}
		return s.cfgSvc.Get().ModelProfiles.ResolveCloudModelForTier(prof, tier)
	}
	if id := s.resolveTierModel(tier); id != "" {
		return id
	}
	return s.resolveTierModel(config.TierEveryday)
}

// watchdogModelFor returns the model for watchdog one-shot checks. Explicit
// watchdog.model config wins. Otherwise the fast_light_text tier's EFFECTIVE
// open model (override-else-catalog-default) is used — the watchdog's oneShot
// lane dispatches to the local co-processor, so it stays strictly on the open
// side. Empty means the lane keeps its own default resolution.
func (s *Server) watchdogModelFor(wc config.WatchdogConfig) string {
	if wc.Model != "" {
		return wc.Model
	}
	return s.resolveTierModel(config.TierFastLightText)
}
