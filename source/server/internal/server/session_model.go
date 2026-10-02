package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/chatroute"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

func (s *Server) sessionModel(ctx context.Context, convID string, req chatroute.Request) (chatroute.Status, error) {
	store, ok := s.persistSvc.Store().(chatroute.Store)
	if !ok {
		return chatroute.Status{}, fmt.Errorf("session model persistence unavailable")
	}
	if convID == "" {
		return chatroute.Status{}, fmt.Errorf("conversation id required")
	}
	if req.Action == "" {
		req.Action = "status"
	}
	switch req.Action {
	case "models":
		if strings.TrimSpace(req.Profile) == "" || req.Model != "" {
			return chatroute.Status{}, fmt.Errorf("models requires a saved profile name and no model")
		}
		catalog, err := s.ListCloudProfileModels(ctx, &proto.ListCloudProfileModelsRequest{ProfileName: req.Profile})
		if err != nil {
			return chatroute.Status{}, err
		}
		if catalog.GetError() != "" {
			return chatroute.Status{}, fmt.Errorf("model catalog: %s", catalog.GetError())
		}
		route, err := store.ChatRoute(ctx, convID)
		if err != nil {
			return chatroute.Status{}, err
		}
		result := chatroute.Status{Override: route, Note: "Available model IDs for " + req.Profile + ". No routing changes made."}
		for _, m := range catalog.Models {
			result.Models = append(result.Models, chatroute.Model{ID: m.GetId(), Name: m.GetDisplayName()})
		}
		return result, nil
	case "set":
		route := chatroute.Route{Profile: strings.TrimSpace(req.Profile), Model: strings.TrimSpace(req.Model)}
		resolver, ok := s.providerSvc.(chatroute.Resolver)
		if !ok {
			return chatroute.Status{}, fmt.Errorf("session model routing unavailable")
		}
		if _, err := resolver.ResolveChatRoute(ctx, route); err != nil {
			return chatroute.Status{}, err
		}
		if err := store.SetChatRoute(ctx, convID, &route); err != nil {
			return chatroute.Status{}, err
		}
	case "clear", "status":
		if req.Profile != "" || req.Model != "" {
			return chatroute.Status{}, fmt.Errorf("profile/model are only valid for set")
		}
		if req.Action == "clear" {
			if err := store.SetChatRoute(ctx, convID, nil); err != nil {
				return chatroute.Status{}, err
			}
		}
	default:
		return chatroute.Status{}, fmt.Errorf("unknown session model action %q (want status, models, set, clear)", req.Action)
	}
	route, err := store.ChatRoute(ctx, convID)
	if err != nil {
		return chatroute.Status{}, err
	}
	result := chatroute.Status{Override: route, Note: "Normal main-chat routing. Changes apply to the next message; delegation and background routing are unchanged."}
	if route != nil {
		result.Note = "Session main-chat override; applies to the next message and persists on resume. No fallback. Delegation and background routing are unchanged. Model availability is checked by the provider on the next request."
	}
	cfg := s.cfgSvc.Get()
	for _, p := range cfg.CloudProfiles {
		result.Profiles = append(result.Profiles, chatroute.Profile{Name: p.Name, Provider: p.Provider, Model: cfg.ModelProfiles.ResolveCloudModelForTier(p, cfg.TaskAssignment(config.TaskChat).Quality.CapabilityTier())})
	}
	return result, nil
}

// SessionModel is a direct user control, usable even when the selected model is
// unavailable. Agent-initiated changes use the permission-gated capability.
func (s *Server) SessionModel(ctx context.Context, req *proto.SessionModelRequest) (*proto.SessionModelResponse, error) {
	if s.persistSvc.Store() == nil {
		return nil, fmt.Errorf("session model persistence unavailable")
	}
	// CLI sessions may not have sent their first message yet.
	if req.GetConversationId() == "" {
		return nil, fmt.Errorf("conversation id required")
	}
	if req.GetAction() == "set" {
		if err := s.persistSvc.Store().EnsureConversation(ctx, req.GetConversationId(), req.GetWorkDir(), ""); err != nil {
			return nil, err
		}
	}
	result, err := s.sessionModel(ctx, req.GetConversationId(), chatroute.Request{Action: req.GetAction(), Profile: req.GetProfile(), Model: req.GetModel()})
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &proto.SessionModelResponse{ResultJson: data}, nil
}
