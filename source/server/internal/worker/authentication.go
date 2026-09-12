package worker

import (
	"context"
	"sync"
	"sync/atomic"

	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

type authenticationResult struct {
	choice llm.AuthDecision
	err    error
}
type streamAuthentication struct {
	sender  *sender
	next    atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan authenticationResult
}

func newStreamAuthentication(s *sender) *streamAuthentication {
	return &streamAuthentication{sender: s, pending: make(map[uint64]chan authenticationResult)}
}
func (s *streamAuthentication) Request(ctx context.Context, c llm.AuthChallenge) (llm.AuthDecision, error) {
	id := s.next.Add(1)
	reply := make(chan authenticationResult, 1)
	s.mu.Lock()
	s.pending[id] = reply
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	s.sender.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_AuthRequest{AuthRequest: &proto.WorkerAuthenticationRequest{Id: id, Challenge: &proto.AuthenticationRequired{Provider: c.Provider, ProfileName: c.Profile, Reason: c.Reason, Fallback: c.Fallback, RetrySafe: c.RetrySafe}}}})
	select {
	case r := <-reply:
		return r.choice, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (s *streamAuthentication) deliver(r *proto.WorkerAuthenticationResponse) {
	s.mu.Lock()
	reply, ok := s.pending[r.GetId()]
	delete(s.pending, r.GetId())
	s.mu.Unlock()
	if !ok {
		return
	}
	result := authenticationResult{choice: llm.AuthDecision(r.GetDecision())}
	if r.GetError() != "" {
		result.err = &llm.CredentialError{Class: llm.ErrCredential, Reason: "recovery_unavailable"}
	}
	reply <- result
}

// A separate RPC is feature negotiation: an older worker must fail closed,
// not ignore a capability flag and silently use its old fallback policy.
func (w *WorkerServer) RunTurnWithAuthentication(stream proto.Worker_RunTurnWithAuthenticationServer) error {
	return w.runTurn(stream, true)
}
