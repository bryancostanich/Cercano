package agenttools

import "context"

type progressEmitterKey struct{}

// ProgressEvent is a structured, proto-free event that a tool may publish while
// executing. It is intentionally owned by agenttools so capability adapters,
// the tool loop, runner, and host can pass sub-agent lifecycle events without a
// package cycle.
type ProgressEvent struct {
	Text string

	TaskChangeKind string
	TaskSnapshot   TaskProgressSnapshot

	SubAgentID       string
	SubAgentParentID string
	SubAgentTitle    string
	Kind             string
	GrantedTools     []string
	IgnoredTools     []string

	ToolUseID   string
	ToolName    string
	ArgsSummary string
	ArgsJSON    string
	Summary     string
	Detail      string
	StartLine   int
	IsError     bool
}

// TaskProgressSnapshot is the progress-channel mirror of a task-store node
// snapshot. Capabilities use it to publish semantic plan/task changes through
// the existing tool-loop progress path without importing runner or proto types.
type TaskProgressSnapshot struct {
	ID       string
	Title    string
	Status   string
	Notes    string
	ParentID string
	Children []TaskProgressSnapshot
}

// ProgressEmitter is a best-effort callback tools may use to surface progress
// while they execute. Structured fields are optional; Text is used for normal
// parent progress and as a fallback label.
type ProgressEmitter func(ProgressEvent)

// WithProgressEmitter returns a context carrying emit for tools executed by the
// agent loop. A nil emitter is allowed and behaves like no emitter.
func WithProgressEmitter(ctx context.Context, emit ProgressEmitter) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, progressEmitterKey{}, emit)
}

// ProgressEmitterFromContext returns the progress callback carried by ctx, if
// one was installed by the tool loop.
func ProgressEmitterFromContext(ctx context.Context) ProgressEmitter {
	if ctx == nil {
		return nil
	}
	if emit, ok := ctx.Value(progressEmitterKey{}).(ProgressEmitter); ok {
		return emit
	}
	return nil
}
