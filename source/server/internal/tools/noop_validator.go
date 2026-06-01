package tools

import "context"

// NoOpValidator skips validation and returns a Skipped decision with a reason.
type NoOpValidator struct {
	reason string
}

// NewNoOpValidator returns a validator that always Skipped with the given reason.
func NewNoOpValidator(reason string) *NoOpValidator {
	return &NoOpValidator{reason: reason}
}

func (v *NoOpValidator) Validate(_ context.Context, _ string) (Decision, error) {
	return Skipped, &SkipReason{Reason: v.reason}
}
