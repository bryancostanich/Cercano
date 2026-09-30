package compaction

import (
	"context"
	"strings"

	"cercano/source/server/internal/llm"
)

type taskReferenceKey struct{}
type userIntentKey struct{}

// WithTaskReference supplies relevance context, not additional material to freeze
// or authority for new approvals. It does not modify the protected task/history.
func WithTaskReference(ctx context.Context, task string) context.Context {
	return context.WithValue(ctx, taskReferenceKey{}, task)
}
func TaskReferenceFrom(ctx context.Context) string {
	task, _ := ctx.Value(taskReferenceKey{}).(string)
	return task
}

// WithUserIntentHint carries the latest user message for callers that need to
// know what the user most recently said. A main thread has no assigned task:
// its trailing message is usually conversational ("push", "continue", "land on
// main"), so presenting it to the summarizer as the worker's objective would
// misdirect fact selection. This value is deliberately never placed in the
// prompt; only an assigned task (WithTaskReference) reaches the summarizer.
func WithUserIntentHint(ctx context.Context, msg string) context.Context {
	return context.WithValue(ctx, userIntentKey{}, msg)
}

// LatestUserMessage returns the most recent genuine user text, ignoring
// tool-result wrappers and generated summary preambles.
func LatestUserMessage(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleUser {
			continue
		}
		var text []string
		for _, b := range messages[i].Blocks {
			if b.Type == llm.BlockText && strings.TrimSpace(b.Text) != "" && !strings.HasPrefix(b.Text, "[conversation summary]") {
				text = append(text, b.Text)
			}
		}
		if len(text) > 0 {
			return strings.Join(text, "\n")
		}
	}
	return ""
}
