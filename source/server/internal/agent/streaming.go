package agent

import (
	"context"
	"iter"

	"google.golang.org/adk/session"
)

// StreamableCoordinator extends Coordinator with event-streaming capability.
type StreamableCoordinator interface {
	Coordinator
	CoordinateStream(ctx context.Context, instruction, inputCode, workDir, fileName string) (
		iter.Seq2[*session.Event, error], func() (*Response, error), error,
	)
}

// TokenFunc defines a callback for incremental token delivery.
type TokenFunc func(token string)

// StreamingModelProvider extends ModelProvider with token-level streaming.
type StreamingModelProvider interface {
	ModelProvider
	ProcessStream(ctx context.Context, req *Request, onToken TokenFunc) (*Response, error)
}

// MapEventToProgress converts an ADK session event to a human-readable progress string.
func MapEventToProgress(event *session.Event) string {
	if event == nil {
		return ""
	}
	switch event.Author {
	case "generator":
		return "Generating code..."
	case "validator":
		if event.Actions.Escalate {
			return "Validation passed."
		}
		return "Validation failed. Retrying..."
	default:
		return ""
	}
}
