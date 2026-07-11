package agent

import (
	"context"
	"sync"
)

type Decision struct {
	Allow   bool
	Persist bool
	// Message, when set on a denial (Allow=false), is the user's "chat about
	// this" redirect. The tool loop records it as the tool_result and CONTINUES
	// the turn instead of ending it, so the model responds to the redirect in
	// the same stream rather than on a fresh turn.
	Message string
}

// FollowUpDenial is the sentinel error a PermissionRequester returns when the
// user declines a tool call but supplies a redirect message ("chat about
// this"). It carries no failure semantics — the tool loop catches it at its
// existing error check, writes Message as the tool_result, and continues the
// turn. Using a sentinel keeps the PermissionRequester signature (and its
// runner/worker mirrors) unchanged.
type FollowUpDenial struct{ Message string }

func (e *FollowUpDenial) Error() string { return "user declined with follow-up message" }

// PendingDecisions is the permission barrier. It is keyed per conversation:
// a waiter registers under (conversationID, toolUseID) and only a Resolve
// carrying the SAME pair delivers the decision. This keeps two live
// conversations isolated even when their tool-use IDs collide (possible with
// local models that synthesize sequential ids).
type PendingDecisions struct {
	mu       sync.Mutex
	channels map[string]chan Decision
}

func NewPendingDecisions() *PendingDecisions {
	return &PendingDecisions{channels: map[string]chan Decision{}}
}

// decisionKey namespaces a tool-use ID by its conversation. The NUL separator
// is unambiguous — neither a conversation ID nor a tool-use ID contains it.
func decisionKey(conversationID, toolUseID string) string {
	return conversationID + "\x00" + toolUseID
}

// Wait blocks until Resolve posts a decision for (conversationID, toolUseID)
// or ctx is cancelled.
func (p *PendingDecisions) Wait(ctx context.Context, conversationID, toolUseID string) (Decision, error) {
	key := decisionKey(conversationID, toolUseID)
	ch := make(chan Decision, 1)
	p.mu.Lock()
	p.channels[key] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.channels, key)
		p.mu.Unlock()
	}()
	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		return Decision{}, ctx.Err()
	}
}

// Resolve delivers a decision to the waiter registered under
// (conversationID, toolUseID). Returns false when no such waiter exists.
func (p *PendingDecisions) Resolve(conversationID, toolUseID string, d Decision) bool {
	key := decisionKey(conversationID, toolUseID)
	p.mu.Lock()
	ch, ok := p.channels[key]
	p.mu.Unlock()
	if !ok {
		return false
	}
	ch <- d
	return true
}
