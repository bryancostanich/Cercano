package llm

import "context"

// sessionContextKey carries the conversation/session identity a provider call
// belongs to. It tags a call with the conversation it serves so cross-cutting
// consumers (the stream-anomaly log) can attribute events to a conversation. A
// call made on behalf of a DIFFERENT conversation than the ctx's originating
// turn (subagent dispatch, one-shot coproc calls) overrides it with its own id
// so events aren't misattributed to the parent.
type sessionContextKey struct{}

// WithSessionID attaches a conversation/session ID to ctx for provider calls.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, id)
}

// SessionIDFromContext returns the session ID attached by WithSessionID, or "".
func SessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sessionContextKey{}).(string); ok {
		return v
	}
	return ""
}
