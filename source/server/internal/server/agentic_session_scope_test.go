package server

import (
	"context"
	"sync"
	"testing"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// sessionCapturingProvider wraps a provider and records the llm session id on
// every Chat/StreamChat ctx, so tests can assert what identity a call carried.
type sessionCapturingProvider struct {
	inference.Provider
	mu   sync.Mutex
	seen []string
}

func (p *sessionCapturingProvider) record(ctx context.Context) {
	p.mu.Lock()
	p.seen = append(p.seen, llm.SessionIDFromContext(ctx))
	p.mu.Unlock()
}

func (p *sessionCapturingProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.record(ctx)
	return p.Provider.Chat(ctx, req)
}

func (p *sessionCapturingProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.record(ctx)
	return p.Provider.StreamChat(ctx, req)
}

func (p *sessionCapturingProvider) sessions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// A dispatched subagent's provider calls must NOT ride the parent
// conversation's session identity. Upstream bridges (Meridian's OpenCode
// adapter) key persistent SDK sessions on it; a subagent's disjoint history
// arriving on the parent's key evicts the parent's session lineage and
// cross-delivers turns between the two. The subagent must carry its own id —
// the sub-conversation id when persistence is available.
func TestRunAgenticDispatch_ScopesProviderSessionToSubagent(t *testing.T) {
	srv, prov := observabilityDispatchRig(t)
	capture := &sessionCapturingProvider{Provider: prov}

	parentCtx := llm.WithSessionID(context.Background(), "parent-session-id")
	res, err := srv.runAgenticDispatch(parentCtx,
		dispatch.Spec{Mode: dispatch.Agentic, Task: "probe task", ConversationID: "parent-conv"},
		dispatch.Selection{Provider: capture}, "test-model")
	if err != nil {
		t.Fatalf("runAgenticDispatch: %v", err)
	}

	sessions := capture.sessions()
	if len(sessions) == 0 {
		t.Fatal("provider never called")
	}
	for i, sid := range sessions {
		if sid == "parent-session-id" {
			t.Errorf("call %d inherited the parent conversation's session id", i)
		}
		if sid == "" {
			t.Errorf("call %d has no session id at all", i)
		}
		if res.SubConversationID != "" && sid != res.SubConversationID {
			t.Errorf("call %d session id = %q, want the sub-conversation id %q", i, sid, res.SubConversationID)
		}
	}
}
