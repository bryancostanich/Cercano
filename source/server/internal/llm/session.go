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

// independentSessionKey marks a call as NOT a continuation of any conversation
// — a dispatch subagent or a one-shot. Providers that multiplex conversations
// can use it to bypass lineage/session matching entirely (belt-and-suspenders
// alongside the unique session id).
type independentSessionKey struct{}

// WithIndependentSession marks ctx as an independent (non-conversational)
// session. On the Meridian route this emits x-meridian-source: subagent-<id>,
// which makes Meridian's OpenCode adapter treat the request as an independent
// session and skip lineage lookup.
func WithIndependentSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, independentSessionKey{}, true)
}

// IsIndependentSession reports whether ctx was marked by WithIndependentSession.
func IsIndependentSession(ctx context.Context) bool {
	v, ok := ctx.Value(independentSessionKey{}).(bool)
	return ok && v
}
