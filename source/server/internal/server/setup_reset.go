package server

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/setupreset"
	"cercano/source/server/pkg/proto"
)

// ResetSetup is an explicitly triggered developer reset, not a maintenance
// fence. Sessions remain connected and in-flight work may fail or write back
// stale state. It is intentionally not exposed as a model-callable tool.
func (s *Server) ResetSetup(ctx context.Context, req *proto.ResetSetupRequest) (*proto.ResetSetupResponse, error) {
	if !req.GetConfirmed() {
		return &proto.ResetSetupResponse{Error: "explicit setup-reset confirmation required"}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := setupreset.Reset(ctx, s.cfgSvc.Path(), s.cfgSvc.Secrets(), s.cfgSvc.Set)
	response := &proto.ResetSetupResponse{Ok: err == nil, CredentialsRemoved: int32(result.DeletedCredentials), ConfigWritten: result.ConfigWritten, LiveApplied: result.LiveApplied}
	if err != nil {
		response.Error = fmt.Sprintf("setup reset incomplete (credentials removed: %d, live settings applied: %t, config written: %t): %v", result.DeletedCredentials, result.LiveApplied, result.ConfigWritten, err)
	}
	if result.LiveApplied {
		c := result.Config
		// Clear cached native/local routing before constructing providers for the
		// reset config. Do not retain a previous selected model when defaults have
		// no available model yet. The existing session/conversation stores are not
		// replaced, drained or closed.
		s.providerSvc.SetOpenLLMProvider(nil)
		var model string
		if s.openModels != nil {
			model = s.openModels.ChatModel()
		}
		s.providerSvc.Reconfigure(providers.ReconfigureArgs{OllamaURL: c.OllamaURL, OpenRuntime: c.OpenRuntime, ResolvedOpenModel: model, MutatedConfig: c})
		s.applyRuntimeEndpoints(c)
		warnings := []string{}
		if rebuildErr := s.rebuildCloud(); rebuildErr != nil {
			warnings = append(warnings, "settings reset; provider unavailable until setup is completed")
		}
		response.Warning = strings.Join(warnings, "; ")
		s.broadcastConfigChanged("setup_reset", "")
		s.broadcastConfigChanged("routing_assignments", "")
	}
	return response, nil
}
