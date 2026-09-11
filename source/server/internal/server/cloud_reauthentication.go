package server

import (
	"context"
	"errors"
	"strings"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/credentials"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReauthenticateCloud is deliberately separate from profile setup. An older
// peer cannot silently ignore a preservation flag and rewrite configuration.
func (s *Server) ReauthenticateCloud(req *proto.CloudReauthenticationRequest, stream proto.Agent_ReauthenticateCloudServer) error {
	return s.runCloudReauthentication(req, stream, anthropicauth.Flow{}, chatgptauth.Flow{})
}
func (s *Server) runCloudReauthentication(req *proto.CloudReauthenticationRequest, stream proto.Agent_ReauthenticateCloudServer, claude anthropicauth.Flow, chatgpt chatgptauth.Flow) error {
	profile := strings.TrimSpace(req.GetProfileName())
	id := req.GetAttemptId()
	if profile == "" || strings.TrimSpace(id) == "" || len(id) > 128 {
		return status.Error(codes.InvalidArgument, "profile and bounded attempt ID are required")
	}
	for _, p := range s.cfgSvc.Get().CloudProfiles {
		if p.Name == profile {
			if !cloudfactory.IsSubscription(p) {
				return status.Error(codes.FailedPrecondition, "profile does not use subscription login")
			}
			if p.Route == cloudfactory.RouteSubscription {
				return s.runClaudeLogin(&proto.StartClaudeLoginRequest{ProfileName: profile}, &claudeReauthenticationStream{Agent_ReauthenticateCloudServer: stream, profile: profile, id: id}, true, claude)
			}
			return s.runChatGPTLogin(&proto.StartChatGPTLoginRequest{ProfileName: profile}, &chatgptReauthenticationStream{Agent_ReauthenticateCloudServer: stream, profile: profile, id: id}, true, chatgpt)
		}
	}
	return status.Error(codes.NotFound, "cloud profile not found")
}

type claudeReauthenticationStream struct {
	proto.Agent_ReauthenticateCloudServer
	profile, id string
}

func (s *claudeReauthenticationStream) Send(e *proto.StartClaudeLoginEvent) error {
	return s.Agent_ReauthenticateCloudServer.Send(&proto.CloudLoginEvent{ProfileName: s.profile, Provider: "anthropic", AttemptId: s.id, AuthorizeUrl: e.GetAuthorizeUrl(), Done: e.GetDone(), Ok: e.GetOk(), Error: e.GetError()})
}

type chatgptReauthenticationStream struct {
	proto.Agent_ReauthenticateCloudServer
	profile, id string
}

func (s *chatgptReauthenticationStream) Send(e *proto.StartChatGPTLoginEvent) error {
	return s.Agent_ReauthenticateCloudServer.Send(&proto.CloudLoginEvent{ProfileName: s.profile, Provider: "openai-responses", AttemptId: s.id, VerificationUrl: e.GetVerificationUrl(), UserCode: e.GetUserCode(), Done: e.GetDone(), Ok: e.GetOk(), Error: e.GetError()})
}

// Do not forward arbitrary OAuth descriptions, callback query parameters or
// transport URLs into a login stream. Structured safe reasons remain useful.
func loginFailure(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, credentials.ErrLoginSuperseded) {
		return "login canceled or superseded"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "login timed out; please try again"
	}
	if errors.Is(err, cfgsvc.ErrLoginProfileChanged) {
		return cfgsvc.ErrLoginProfileChanged.Error()
	}
	var credential *llm.CredentialError
	if errors.As(err, &credential) {
		return credential.Error()
	}
	var endpoint *llm.TokenEndpointError
	if errors.As(err, &endpoint) {
		return endpoint.Error()
	}
	return "login failed; check the profile and try again"
}
