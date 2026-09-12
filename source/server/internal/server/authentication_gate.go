package server

import (
	"cercano/source/server/pkg/config"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type authenticationWaiter struct {
	routing   [32]byte
	ctx       context.Context
	challenge llm.AuthChallenge
	reply     chan llm.AuthDecision
}

func (s *Server) authenticationRequester(conversation string, sink runner.EventSink) llm.AuthRequester {
	routing := authenticationRoutingIdentity(s.cfgSvc.Get())
	return func(ctx context.Context, challenge llm.AuthChallenge) (llm.AuthDecision, error) {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", err
		}
		id := hex.EncodeToString(nonce[:])
		key := conversation + "\x00" + id
		waiter := &authenticationWaiter{routing: routing, ctx: ctx, challenge: challenge, reply: make(chan llm.AuthDecision, 1)}
		completedLogin := s.cfgSvc.Credentials().WatchLogin(challenge.Profile, challenge.Provider)
		s.authenticationWaiters.Store(key, waiter)
		defer s.authenticationWaiters.Delete(key)
		sink.Emit(runner.Event{Kind: runner.EventAuthentication, Authentication: &runner.Authentication{ConversationID: conversation, RequestID: id, Challenge: challenge}})
		finish := func(choice llm.AuthDecision) (llm.AuthDecision, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			sink.Emit(runner.Event{Kind: runner.EventAuthentication, Authentication: &runner.Authentication{ConversationID: conversation, RequestID: id, Challenge: challenge, Resolved: true}})
			if choice != llm.AuthCancel && routing != authenticationRoutingIdentity(s.cfgSvc.Get()) {
				return "", &llm.CredentialError{Class: llm.ErrCredential, Provider: challenge.Provider, Profile: challenge.Profile, Reason: "routing changed while paused; submit a fresh request"}
			}
			return choice, nil
		}
		select {
		case choice := <-waiter.reply:
			return finish(choice)
		case <-completedLogin:
			// An explicit decision that already claimed the waiter wins over an
			// overlapping login broadcast. Do not randomly replace fallback/cancel.
			if s.authenticationWaiters.CompareAndDelete(key, waiter) {
				return finish(llm.AuthLogin)
			}
			select {
			case choice := <-waiter.reply:
				return finish(choice)
			case <-ctx.Done():
				return "", ctx.Err()
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}

	}
}
func (s *Server) ResolveAuthentication(ctx context.Context, req *proto.AuthenticationDecisionRequest) (*proto.AuthenticationDecisionResponse, error) {
	key := req.GetConversationId() + "\x00" + req.GetRequestId()
	value, ok := s.authenticationWaiters.Load(key)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "authentication request is no longer active")
	}
	waiter := value.(*authenticationWaiter)
	if waiter.ctx.Err() != nil {
		return nil, status.Error(codes.FailedPrecondition, "authentication request was canceled")
	}
	decision := llm.AuthDecision(req.GetDecision())
	switch decision {
	case llm.AuthCancel:
	case llm.AuthFallback:
		if waiter.routing != authenticationRoutingIdentity(s.cfgSvc.Get()) {
			if s.authenticationWaiters.CompareAndDelete(key, waiter) {
				waiter.reply <- llm.AuthCancel
			}
			return nil, status.Error(codes.FailedPrecondition, "routing changed while authentication was paused; submit a fresh request")
		}
		if waiter.challenge.Fallback == "" || !waiter.challenge.RetrySafe {
			return nil, status.Error(codes.PermissionDenied, "fallback is not available for this request")
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown authentication decision")
	}
	if !s.authenticationWaiters.CompareAndDelete(key, waiter) {
		return nil, status.Error(codes.FailedPrecondition, "authentication request already resolved")
	}
	waiter.reply <- decision
	return &proto.AuthenticationDecisionResponse{}, nil
}

// Credentials are deliberately absent: completing login must not invalidate
// its own gate. Compare routing configuration, not provider error prose.
func authenticationRoutingIdentity(c config.Config) [32]byte {
	data, _ := json.Marshal(map[string]any{"active": c.ActiveCloudProfile, "backup": c.BackupCloudProfile, "profiles": c.CloudProfiles, "models": c.ModelProfiles, "locus": c.LocusMode, "runtime": c.OpenRuntime, "ollama_url": c.OllamaURL})
	return sha256.Sum256(data)
}
