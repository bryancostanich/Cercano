package worker

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/proto"
)

func TestModelAuthorizationNeedsMatchingLiveResponse(t *testing.T) {
	for _, mode := range []string{"allow", "deny", "wrong_id", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			wire := make(chan *proto.WorkerToHost, 1)
			authority := newStreamModelAuthority(&sender{ch: wire})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- authority.Authorize(ctx, modelpolicy.Attempt{Provider: "openai", Endpoint: "https://api.example.com/v1", Model: "model", Placement: "external"})
			}()
			request := (<-wire).GetModelAuthorizationRequest()
			switch mode {
			case "allow":
				authority.deliver(&proto.WorkerModelAuthorizationResponse{Id: request.Id, Allowed: true})
			case "deny":
				authority.deliver(&proto.WorkerModelAuthorizationResponse{Id: request.Id})
			case "wrong_id":
				authority.deliver(&proto.WorkerModelAuthorizationResponse{Id: request.Id + 1, Allowed: true})
				cancel()
			case "cancel":
				cancel()
			}
			if err := <-done; (err == nil) != (mode == "allow") {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
			authority.mu.Lock()
			remaining := len(authority.pending)
			authority.mu.Unlock()
			if remaining != 0 {
				t.Fatal("pending authorization leaked")
			}
			// A late or duplicate acknowledgment cannot authorize another request.
			authority.deliver(&proto.WorkerModelAuthorizationResponse{Id: request.Id, Allowed: true})
		})
	}
}

func TestProxiedLocalPolicyDenialRemainsTerminal(t *testing.T) {
	reader := newTestReader(context.Background())
	reader.p.pending[reader.id] = reader
	reader.p.deliver(&proto.OpenInferenceEvent{Id: reader.id, EnterprisePolicyDenied: true, Kind: &proto.OpenInferenceEvent_Error{Error: "denied"}})
	_, _, err := reader.Next()
	if !modelpolicy.IsDenial(err) || llm.ClassOf(err) != llm.ErrPermission {
		t.Fatalf("lost terminal policy failure: %v", err)
	}
}
