package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/proto"
)

// A managed worker has no enterprise tokens or reusable authorization grants.
// Every physical attempt waits for the host's current policy decision.
type streamModelAuthority struct {
	sender  *sender
	next    atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan bool
}

func newStreamModelAuthority(s *sender) *streamModelAuthority {
	return &streamModelAuthority{sender: s, pending: make(map[uint64]chan bool)}
}

func (s *streamModelAuthority) Authorize(parent context.Context, a modelpolicy.Attempt) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	id := s.next.Add(1)
	reply := make(chan bool, 1)
	s.mu.Lock()
	s.pending[id] = reply
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	request := &proto.WorkerToHost{Msg: &proto.WorkerToHost_ModelAuthorizationRequest{ModelAuthorizationRequest: &proto.WorkerModelAuthorizationRequest{Id: id, Provider: a.Provider, Endpoint: a.Endpoint, Model: a.Model, Placement: a.Placement}}}
	select {
	case s.sender.ch <- request:
	case <-ctx.Done():
		return modelpolicy.Deny(a, "host authorization unavailable")
	}
	select {
	case allowed := <-reply:
		if allowed && ctx.Err() == nil {
			return nil
		}
		return modelpolicy.Deny(a, "host did not authorize this model request")
	case <-ctx.Done():
		return modelpolicy.Deny(a, "host authorization unavailable")
	}
}

func (s *streamModelAuthority) deliver(response *proto.WorkerModelAuthorizationResponse) {
	s.mu.Lock()
	reply, ok := s.pending[response.GetId()]
	delete(s.pending, response.GetId())
	s.mu.Unlock()
	if ok {
		reply <- response.GetAllowed()
	}
}

// This separate method is required capability negotiation. Never retry a managed
// turn through RunTurn or RunTurnWithAuthentication if this RPC is unavailable.
func (w *WorkerServer) RunManagedTurn(stream proto.Worker_RunManagedTurnServer) error {
	return w.runTurn(stream, true, true)
}

func (w *WorkerServer) RunManagedTurnWithSettings(stream proto.Worker_RunManagedTurnWithSettingsServer) error {
	return w.runTurn(stream, true, true)
}

// Explicit request choices require this capability. Older workers must fail
// closed instead of silently dropping the choice and using the task default.
func (w *WorkerServer) RunManagedTurnWithSelection(stream proto.Worker_RunManagedTurnWithSelectionServer) error {
	return w.runTurn(stream, true, true)
}
