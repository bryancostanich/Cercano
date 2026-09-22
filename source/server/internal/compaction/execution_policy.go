package compaction

import (
	"context"
	"time"
)

// ExecutionTimeout bounds a complete compaction pass, including all segment
// summaries and provider failover. Shared by scheduled, inline and manual work.
const ExecutionTimeout = 6 * time.Minute

// WithExecutionBudget never extends a caller deadline or detaches cancellation.
func WithExecutionBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ExecutionTimeout)
}
