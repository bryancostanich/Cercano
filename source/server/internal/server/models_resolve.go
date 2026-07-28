package server

import (
	"cercano/source/server/pkg/config"
)

// resolveTierModel resolves a taxonomy tier's OPEN model against the live
// config. Cloud is resolved separately through the vendor-keyed cost-tier path
// (see DispatchModelFor / ModelProfiles.ResolveCloudModelForTier), so this
// reads only the open slot; unconfigured means !ok and the caller decides.
//
// Callers must hold no lock expectations — this reads a snapshot from cfgSvc.
func (s *Server) resolveTierModel(t config.Tier) (string, bool) {
	return s.cfgSvc.Get().Models.ResolveOpen(t)
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
	if id, ok := s.resolveTierModel(tier); ok {
		return id
	}
	if id, ok := s.resolveTierModel(config.TierEveryday); ok {
		return id
	}
	return ""
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
	if id, ok := mc.ResolveOpen(config.TierFastLightText); ok {
		return id
	}
	return ""
}
