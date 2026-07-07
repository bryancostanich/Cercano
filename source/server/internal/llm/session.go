package llm

import "context"

// sessionContextKey carries the conversation/session identity a provider call
// belongs to. Providers that multiplex conversations over a shared upstream
// (the Meridian OpenCode adapter keys persistent SDK sessions on it) use this
// to keep each conversation's lineage isolated. A call made on behalf of a
// DIFFERENT conversation than the ctx's originating turn (subagent dispatch,
// one-shot coproc calls) MUST override this — inheriting the parent's identity
// with a divergent message history poisons the parent's upstream session state
// (evictions, replays, cross-delivered content).
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
