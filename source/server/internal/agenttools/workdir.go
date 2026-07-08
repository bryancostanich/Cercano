package agenttools

import "context"

type workDirKey struct{}

// WithWorkDir attaches the turn's working directory to ctx so tool execution
// resolves relative paths against it instead of the process cwd. Empty dir is a
// no-op (falls back to process cwd — the pre-isolation behavior).
func WithWorkDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, workDirKey{}, dir)
}

// WorkDirFromContext returns the working directory attached by WithWorkDir, or "".
func WorkDirFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(workDirKey{}).(string); ok {
		return v
	}
	return ""
}
