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
	cfg := s.cfgSvc.Get()
	active := cfg.ActiveCloudProfile
	backup := cfg.BackupCloudProfile
	profiles := cfg.CloudProfiles

	// Map config profiles onto the grouping input (order preserved) and keep a
	// name→profile index so we can recover Model when emitting each profile.
	refs := make([]cloudcatalog.ProfileRef, len(profiles))
	byName := make(map[string]config.CloudProfile, len(profiles))
	for i, p := range profiles {
		refs[i] = cloudcatalog.ProfileRef{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseURL: p.BaseURL, Route: p.Route}
		byName[p.Name] = p
	}
	grouped, custom := cloudcatalog.Group(refs, active)

	// Stored-key presence comes from the keychain key NAMES (a prompt-free
	// list) instead of reading each secret, which on macOS raises a Keychain
	// authorization prompt every time the Cloud settings tab is opened.
	keyNamesForPresence := map[string]bool{}
	if st := s.cfgSvc.Secrets(); st != nil {
		if names, err := st.List(); err == nil {
			for _, n := range names {
				keyNamesForPresence[n] = true
			}
		}
	}
	// toInfo builds a proto CloudProfileInfo for a grouped ref, filling Model
	// from the stored profile and checking the keychain for a key (prompt-free
	// presence via the name list above; mirrors GetCloudProfiles).
	toInfo := func(ref cloudcatalog.ProfileRef) *proto.CloudProfileInfo {
		p := byName[ref.Name]
		hasKey := keyNamesForPresence[p.Name]
		return &proto.CloudProfileInfo{
			Name: p.Name, Flavor: p.Flavor, BaseUrl: p.BaseURL, Model: p.Model,
			HasKey: hasKey, Backend: p.Backend, Route: p.Route,
		}
	}

	out := &proto.GetCloudProvidersResponse{Active: active, Backup: backup}
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
