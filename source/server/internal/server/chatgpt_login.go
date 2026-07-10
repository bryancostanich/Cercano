// Package server — StartChatGPTLogin RPC handler.
//
// Server side of the ChatGPT subscription sign-in. The client opens a stream;
// the agent starts OpenAI's device-authorization flow and immediately sends a
// frame carrying the user_code + verification_url for the client to display,
// then blocks polling for the user's approval. On success it stores the token
// set in the keychain under the profile name, creates/updates a
// responses+chatgpt cloud profile, optionally activates it, and sends a
// terminal frame (done=true, ok=true). Failures send a terminal frame with
// ok=false and the reason. Mirrors the InstallOpenRuntime streaming shape.
package server

import (
	"strings"

	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// defaultChatGPTModel is set on a freshly signed-in subscription profile. The
// ChatGPT-account codex backend REJECTS -codex-suffixed model names with
// "model not supported when using Codex with a ChatGPT account"; the plain
// gpt-5.x names are what it accepts. Verified live against the backend. The
// user can change it from the model field afterward.
const defaultChatGPTModel = "gpt-5.5"

// StartChatGPTLogin implements proto.AgentServer.
func (s *Server) StartChatGPTLogin(req *proto.StartChatGPTLoginRequest, stream proto.Agent_StartChatGPTLoginServer) error {
	ctx := stream.Context()
	st := s.cfgSvc.Secrets()
	if st == nil {
		return sendChatGPTLoginResult(stream, false, "", "", "keychain unavailable")
	}
	profile := strings.TrimSpace(req.GetProfileName())
	if profile == "" {
		profile = "chatgpt"
	}
	model := strings.TrimSpace(req.GetModel())
	// The ChatGPT-account backend only accepts gpt-5.x model names. Guard against
	// a cross-provider model (e.g. the global default gemini-3-flash) leaking in
	// from the settings draft: fall back to the ChatGPT default rather than
	// persisting an invalid model onto the profile.
	if !strings.HasPrefix(model, "gpt") {
		model = defaultChatGPTModel
	}

	// Start the device authorization and show the user the code + URL.
	pending, err := chatgptauth.Flow{}.Start(ctx)
	if err != nil {
		return sendChatGPTLoginResult(stream, false, "", "", err.Error())
	}
	if err := stream.Send(&proto.StartChatGPTLoginEvent{
		VerificationUrl: pending.VerificationURL,
		UserCode:        pending.UserCode,
	}); err != nil {
		return err
	}

	// Block until the user approves in their browser (or ctx cancels).
	ts, err := pending.Poll(ctx)
	if err != nil {
		return sendChatGPTLoginResult(stream, false, "", "", err.Error())
	}

	// Persist the token set under the profile name (same keychain slot API
	// keys use — a stored blob is the "signed in" signal).
	if err := chatgptauth.Save(st, profile, *ts); err != nil {
		return sendChatGPTLoginResult(stream, false, "", "", err.Error())
	}

	// Create/replace the responses+chatgpt profile carrying the route.
	np := config.CloudProfile{
		Name:   profile,
		Flavor: cloudfactory.FlavorResponses,
		Route:  cloudfactory.RouteChatGPT,
		Model:  model,
	}
	_, isActive := s.cfgSvc.UpsertProfile(np)
	if req.GetSetActive() {
		s.cfgSvc.SetActiveProfile(profile)
		isActive = true
	}

	if isActive {
		if err := s.rebuildCloud(); err != nil {
			s.persistConfig()
			return sendChatGPTLoginResult(stream, false, profile, ts.AccountID, err.Error())
		}
		s.broadcastConfigChanged("cloud_model", np.Model)
	}
	s.persistConfig()
	return sendChatGPTLoginResult(stream, true, profile, ts.AccountID, "")
}

// sendChatGPTLoginResult emits the terminal frame of the sign-in stream.
func sendChatGPTLoginResult(stream proto.Agent_StartChatGPTLoginServer, ok bool, profile, accountID, errMsg string) error {
	return stream.Send(&proto.StartChatGPTLoginEvent{
		Done:        true,
		Ok:          ok,
		ProfileName: profile,
		AccountId:   accountID,
		Error:       errMsg,
	})
}
