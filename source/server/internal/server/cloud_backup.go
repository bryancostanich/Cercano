package server

import (
	"context"
	"fmt"
	"log"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/fallback"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// wrapBackupLocked wraps the freshly built active-profile provider in a
// fallback composite when a backup profile is configured and buildable.
// cfg is the config snapshot already held by the caller (avoids a second Get()).
// A backup that can't be built is reported and skipped — a broken backup must
// never take down a working primary, so every failure path returns the primary unchanged.
func (s *Server) wrapBackupLocked(primary llm.Provider, primaryName string, cfg config.Config) llm.Provider {
	name := cfg.BackupCloudProfile
	if name == "" || name == primaryName {
		return primary
	}
	bp, ok := profileByName(cfg.CloudProfiles, name)
	if !ok {
		log.Printf("[cloud] backup profile %q not found; running without fallback", name)
		return primary
	}
	st := s.cfgSvc.Secrets()
	key := ""
	if st != nil {
		if k, err := st.Get(bp.Name); err == nil {
			key = k
		}
	}
	// Same authentication carve-outs as the primary build in
	// rebuildCloudLocked: a proxy BaseURL (Meridian) authenticates with no
	// key, and bedrock uses the AWS credential chain.
	if key == "" && bp.BaseURL == "" && bp.Flavor != cloudfactory.FlavorBedrock {
		log.Printf("[cloud] backup profile %q has no API key; running without fallback", name)
		return primary
	}
	var opts cloudfactory.Options
	if bp.Flavor == cloudfactory.FlavorResponses && bp.Route == cloudfactory.RouteChatGPT {
		opts.TokenSource = chatgptauth.NewSource(st, bp.Name, chatgptauth.Flow{})
	}
	if bp.Flavor == cloudfactory.FlavorMessages && bp.Route == cloudfactory.RouteSubscription {
		opts.AnthropicTokenSource = anthropicauth.NewSource(st, bp.Name, anthropicauth.Flow{})
	}
	backup, err := cloudfactory.BuildCloudProvider(bp, key, opts)
	if err != nil {
		log.Printf("[cloud] backup profile %q unbuildable (%v); running without fallback", name, err)
		return primary
	}
	return fallback.New(primary, backup, bp.Model, func(stage string, ferr error) {
		log.Printf("[cloud] failover to backup %q (%s): primary error: %v", name, stage, ferr)
	})
}

// SetBackupCloudProfile implements proto.AgentServer — names the fallback
// profile (empty clears it) and rewires the provider chain. Mirrors
// SetActiveCloudProfile's shape: validate, mutate, rebuild, persist.
func (s *Server) SetBackupCloudProfile(ctx context.Context, req *proto.SetBackupCloudProfileRequest) (*proto.SetBackupCloudProfileResponse, error) {
	name := req.GetName()
	if name != "" {
		exists, isActive := s.cfgSvc.ProfileInfo(name)
		if !exists {
			return &proto.SetBackupCloudProfileResponse{Ok: false, Error: fmt.Sprintf("no profile %q", name)}, nil
		}
		if isActive {
			return &proto.SetBackupCloudProfileResponse{Ok: false, Error: "backup profile cannot be the active profile"}, nil
		}
	}
	s.cfgSvc.SetBackupProfile(name)
	if err := s.rebuildCloud(); err != nil {
		// The backup name is set but the provider chain couldn't be rebuilt —
		// report it, keep the config (mirrors SetActiveCloudProfile).
		s.persistConfig()
		return &proto.SetBackupCloudProfileResponse{Ok: false, Error: err.Error()}, nil
	}
	s.persistConfig()
	return &proto.SetBackupCloudProfileResponse{Ok: true}, nil
}
