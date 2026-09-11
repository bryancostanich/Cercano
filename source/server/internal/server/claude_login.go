// Package server — StartClaudeLogin RPC handler.
//
// Server side of the Claude subscription sign-in. The client opens a stream;
// the agent starts the PKCE loopback flow and immediately sends a frame
// carrying the authorize URL for the client to open in a browser, then blocks
// on the loopback redirect. On success it stores the token set in the keychain
// under the profile name, creates/updates a messages+subscription cloud
// profile, optionally activates it, and sends a terminal frame (done=true,
// ok=true). Failures send a terminal frame with ok=false and the reason.
// Mirrors StartChatGPTLogin (the ChatGPT device-auth sibling).
package server

import (
	"strings"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// StartClaudeLogin implements proto.AgentServer.
func (s *Server) StartClaudeLogin(req *proto.StartClaudeLoginRequest, stream proto.Agent_StartClaudeLoginServer) error {
	return s.runClaudeLogin(req, stream, false, anthropicauth.Flow{})
}

func (s *Server) runClaudeLogin(req *proto.StartClaudeLoginRequest, stream proto.Agent_StartClaudeLoginServer, reauthenticate bool, flow anthropicauth.Flow) error {
	ctx := stream.Context()
	st := s.cfgSvc.Secrets()
	if st == nil {
		return sendClaudeLoginResult(stream, false, "", "keychain unavailable")
	}
	profile := strings.TrimSpace(req.GetProfileName())
	canonicalProfile := profile == ""
	if canonicalProfile {
		profile = "claude"
	}
	model := strings.TrimSpace(req.GetModel())
	modelPinned := model != ""

	np := config.CloudProfile{Name: profile, Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription, Model: model, ModelPinned: modelPinned}
	login, err := s.cfgSvc.BeginCloudLogin(ctx, np, shouldActivateClaudeLogin(req.GetSetActive(), canonicalProfile), reauthenticate)
	if err != nil {
		return sendClaudeLoginResult(stream, false, profile, loginFailure(err))
	}
	defer login.Close()
	ctx = login.Context()

	// Start the loopback authorize and show the user the URL to open.
	pending, err := flow.Start(ctx)
	if err != nil {
		return sendClaudeLoginResult(stream, false, "", loginFailure(err))
	}
	// Own the listener immediately, including failure before Wait is reached.
	defer pending.Close()
	if err := stream.Send(&proto.StartClaudeLoginEvent{
		AuthorizeUrl: pending.AuthorizeURL,
	}); err != nil {
		return err
	}

	// Block until the user approves in their browser (or ctx cancels). Wait
	// catches the loopback redirect, renders the success page, and exchanges
	// the code for a token set.
	ts, err := pending.Wait(ctx)
	if err != nil {
		return sendClaudeLoginResult(stream, false, "", loginFailure(err))
	}

	encoded, err := ts.Encode()
	if err != nil {
		return sendClaudeLoginResult(stream, false, profile, loginFailure(err))
	}
	result, err := login.Commit(encoded)
	if err != nil {
		return sendClaudeLoginResult(stream, false, profile, loginFailure(err))
	}
	if result.Reauthenticated {
		return sendClaudeLoginResult(stream, true, profile, "")
	}
	isActive := result.Active
	np = result.Profile

	if isActive {
		if err := s.rebuildCloud(); err != nil {
			s.persistConfig()
			return sendClaudeLoginResult(stream, false, profile, loginFailure(err))
		}
		s.broadcastConfigChanged("active_cloud_profile", profile)
		s.broadcastConfigChanged("cloud_model", np.Model)
	}
	s.persistConfig()
	return sendClaudeLoginResult(stream, true, profile, "")
}

// shouldActivateClaudeLogin preserves the explicit set_active request while
// making the canonical no-profile "sign in with Claude" path activate by
// default. Older/stale clients can omit set_active and still get the onboarding
// behavior users expect; explicit named-profile reauth remains non-activating
// unless requested.
func shouldActivateClaudeLogin(setActive, canonicalProfile bool) bool {
	return setActive || canonicalProfile
}

// sendClaudeLoginResult emits the terminal frame of the sign-in stream.
func sendClaudeLoginResult(stream proto.Agent_StartClaudeLoginServer, ok bool, profile, errMsg string) error {
	return stream.Send(&proto.StartClaudeLoginEvent{
		Done:        true,
		Ok:          ok,
		ProfileName: profile,
		Error:       errMsg,
	})
}
