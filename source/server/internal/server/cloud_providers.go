package server

import (
	"context"

	"cercano/source/server/internal/cloudcatalog"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// GetCloudProviders implements proto.AgentServer — returns the known-provider
// catalog with each configured profile grouped under its provider (primary
// first). Provider knowledge lives in the cloudcatalog package; this handler
// joins that grouping with the live profile store, keychain presence, and the
// Meridian snapshot. It is additive — GetCloudProfiles is left untouched for
// its existing callers (wizard, runtime-tier resolution).
func (s *Server) GetCloudProviders(ctx context.Context, req *proto.GetCloudProvidersRequest) (*proto.GetCloudProvidersResponse, error) {
	s.cfgMu.RLock()
	active := s.currentConfig.ActiveCloudProfile
	profiles := append([]config.CloudProfile(nil), s.currentConfig.CloudProfiles...)
	s.cfgMu.RUnlock()

	// Map config profiles onto the grouping input (order preserved) and keep a
	// name→profile index so we can recover Model when emitting each profile.
	refs := make([]cloudcatalog.ProfileRef, len(profiles))
	byName := make(map[string]config.CloudProfile, len(profiles))
	for i, p := range profiles {
		refs[i] = cloudcatalog.ProfileRef{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseURL: p.BaseURL, Route: p.Route}
		byName[p.Name] = p
	}
	grouped, custom := cloudcatalog.Group(refs, active)

	// toInfo builds a proto CloudProfileInfo for a grouped ref, filling Model
	// from the stored profile and checking the keychain for a key (mirrors the
	// GetCloudProfiles handler's per-profile logic).
	toInfo := func(ref cloudcatalog.ProfileRef) *proto.CloudProfileInfo {
		p := byName[ref.Name]
		hasKey := false
		if s.secrets != nil {
			if _, err := s.secrets.Get(p.Name); err == nil {
				hasKey = true
			}
		}
		return &proto.CloudProfileInfo{
			Name: p.Name, Flavor: p.Flavor, BaseUrl: p.BaseURL, Model: p.Model,
			HasKey: hasKey, Backend: p.Backend, Route: p.Route,
		}
	}

	out := &proto.GetCloudProvidersResponse{Active: active}
	for _, gp := range grouped {
		cp := &proto.CloudProvider{
			Id: gp.ID, Label: gp.Label, Flavor: gp.Flavor, Backend: gp.Backend,
			BaseUrl: gp.BaseURL, Tier: string(gp.Tier), PrimaryProfile: gp.Primary,
		}
		for _, ref := range gp.Profiles {
			cp.Profiles = append(cp.Profiles, toInfo(ref))
		}
		out.Providers = append(out.Providers, cp)
	}
	for _, ref := range custom {
		out.CustomProfiles = append(out.CustomProfiles, toInfo(ref))
	}

	if s.meridianMgr != nil {
		out.MeridianStatus = meridianStatusToProto(s.meridianMgr.Status())
	} else {
		out.MeridianStatus = &proto.MeridianStatus{State: "disabled"}
	}
	return out, nil
}
