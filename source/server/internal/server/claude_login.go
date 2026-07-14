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

// defaultClaudeModel is set on a freshly signed-in subscription profile. The
// user can change it from the model field afterward.
const defaultClaudeModel = "claude-sonnet-5"

// StartClaudeLogin implements proto.AgentServer.
func (s *Server) StartClaudeLogin(req *proto.StartClaudeLoginRequest, stream proto.Agent_StartClaudeLoginServer) error {
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
	if model == "" {
		model = defaultClaudeModel
	}

	// Start the loopback authorize and show the user the URL to open.
	pending, err := anthropicauth.Flow{}.Start(ctx)
	if err != nil {
		return sendClaudeLoginResult(stream, false, "", err.Error())
	}
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
		return sendClaudeLoginResult(stream, false, "", err.Error())
	}

	// Persist the token set under the profile name (same keychain slot API
	// keys use — a stored blob is the "signed in" signal).
	if err := anthropicauth.Save(st, profile, *ts); err != nil {
		return sendClaudeLoginResult(stream, false, "", err.Error())
	}

	// Create/replace the messages+subscription profile carrying the route.
	np := config.CloudProfile{
		Name:   profile,
		Flavor: cloudfactory.FlavorMessages,
		Route:  cloudfactory.RouteSubscription,
		Model:  model,
	}
	_, isActive := s.cfgSvc.UpsertProfile(np)
	if shouldActivateClaudeLogin(req.GetSetActive(), canonicalProfile) {
		s.cfgSvc.SetActiveProfile(profile)
		isActive = true
	}

	if isActive {
		if err := s.rebuildCloud(); err != nil {
			s.persistConfig()
			return sendClaudeLoginResult(stream, false, profile, err.Error())
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
