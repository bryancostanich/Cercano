package agenttools

import "context"

type convIDKey struct{}

// WithConversationID attaches the turn's conversation id to ctx so tool
// execution (notably dispatch) can link work back to the calling
// conversation without trusting the model to pass its own id as an argument.
// Empty id is a no-op.
func WithConversationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, convIDKey{}, id)
}

// ConversationIDFromContext returns the id attached by WithConversationID, or "".
func ConversationIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(convIDKey{}).(string); ok {
		return v
	}
	return ""
}
