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

// StartChatGPTLogin implements proto.AgentServer.
func (s *Server) StartChatGPTLogin(req *proto.StartChatGPTLoginRequest, stream proto.Agent_StartChatGPTLoginServer) error {
	return s.runChatGPTLogin(req, stream, false, chatgptauth.Flow{})
}

func (s *Server) runChatGPTLogin(req *proto.StartChatGPTLoginRequest, stream proto.Agent_StartChatGPTLoginServer, reauthenticate bool, flow chatgptauth.Flow) error {
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
	if model == "" {
		model = config.Defaults().ModelProfiles.ResolveCloudModelForTier(config.CloudProfile{
			Flavor: cloudfactory.FlavorResponses,
			Route:  cloudfactory.RouteChatGPT,
		}, config.TierEveryday)
	}

	np := config.CloudProfile{Name: profile, Flavor: cloudfactory.FlavorResponses, Route: cloudfactory.RouteChatGPT, Model: model, ModelPinned: strings.TrimSpace(req.GetModel()) != ""}
	login, err := s.cfgSvc.BeginCloudLogin(ctx, np, req.GetSetActive(), reauthenticate, req.GetCreateOnly())
	if err != nil {
		return sendChatGPTLoginResult(stream, false, profile, "", loginFailure(err))
	}
	defer login.Close()
	ctx = login.Context()

	// Start the device authorization and show the user the code + URL.
	pending, err := flow.Start(ctx)
	if err != nil {
		return sendChatGPTLoginResult(stream, false, "", "", loginFailure(err))
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
		return sendChatGPTLoginResult(stream, false, "", "", loginFailure(err))
	}

	encoded, err := ts.Encode()
	if err != nil {
		return sendChatGPTLoginResult(stream, false, profile, "", loginFailure(err))
	}
	result, err := login.Commit(encoded)
	if err != nil {
		return sendChatGPTLoginResult(stream, false, profile, "", loginFailure(err))
	}
	if result.Reauthenticated {
		return sendChatGPTLoginResult(stream, true, profile, ts.AccountID, "")
	}
	isActive := result.Active
	np = result.Profile

	if isActive {
		if err := s.rebuildCloud(); err != nil {
			s.persistConfig()
			return sendChatGPTLoginResult(stream, false, profile, ts.AccountID, loginFailure(err))
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
