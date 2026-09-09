package llm

import "fmt"

// LocalStartupError marks a local runtime startup/readiness failure before a
// model request was submitted. It is deliberately separate from generic HTTP
// and inference failures: callers may retry this request, but not a tool loop.
// Context cancellation remains discoverable through Unwrap and must not cause
// cross-tier fallback.
type LocalStartupError struct {
	Provider string
	Model    string
	Err      error
}

func (e *LocalStartupError) Error() string {
	return fmt.Sprintf("%s startup for %s: %v", e.Provider, e.Model, e.Err)
}
func (e *LocalStartupError) Unwrap() error { return e.Err }
