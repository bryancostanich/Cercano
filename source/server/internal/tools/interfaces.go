package tools

import "context"

// CodeGenerator defines the interface for generating and fixing code.
type CodeGenerator interface {
	Generate(ctx context.Context, instruction string, code string) (string, error)
	Fix(ctx context.Context, code string, errorMsg string) (string, error)
}

// Decision is the outcome of a Validate call.
type Decision int

const (
	// Passed: validation succeeded.
	Passed Decision = iota
	// Failed: validation ran and returned a non-zero status; the returned error
	// contains the output to be fed back to the LLM.
	Failed
	// Skipped: no validation was performed; the returned error is a *SkipReason
	// the coordinator should surface to the user. Skipped MUST NOT trigger retries.
	Skipped
)

func (d Decision) String() string {
	switch d {
	case Passed:
		return "passed"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// SkipReason is the sentinel error returned alongside a Skipped decision. It
// lets the coordinator type-assert and pull the message into the streamed output.
type SkipReason struct {
	Reason string
}

func (s *SkipReason) Error() string { return s.Reason }

// Validator runs validation logic in the specified directory.
type Validator interface {
	Validate(ctx context.Context, workDir string) (Decision, error)
}
