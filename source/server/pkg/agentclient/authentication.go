package agentclient

import (
	"cercano/source/server/pkg/proto"
	"context"
)

type AuthenticationRequired struct {
	ConversationID, RequestID, Provider, Profile, Reason, Fallback string
	RetrySafe                                                      bool
	Resolved                                                       bool
}

func (c *Client) ResolveAuthentication(ctx context.Context, conversation, id, decision string) error {
	_, err := c.agent.ResolveAuthentication(ctx, &proto.AuthenticationDecisionRequest{ConversationId: conversation, RequestId: id, Decision: decision})
	return err
}

// Explicit opt-in: noninteractive StreamChat callers keep failing closed rather
// than receiving a prompt they cannot answer.
func (c *Client) StreamChatWithRecovery(ctx context.Context, conversation, input, workDir string, images ...InlineImage) (<-chan StreamMsg, error) {
	return c.streamChat(ctx, conversation, input, workDir, true, images...)
}
