package agent

// CloudAbsentError is returned by a ModelProvider that stands in for an
// unconfigured cloud route. The Agent intercepts this and auto-degrades the
// turn to the local provider while emitting a visible progress notice to the
// caller — silent mock fallbacks are explicitly forbidden by the CLI spec.
type CloudAbsentError struct {
	Reason string // one-line user-facing explanation
}

func (e *CloudAbsentError) Error() string {
	if e.Reason == "" {
		return "cloud provider not configured"
	}
	return "cloud provider not configured: " + e.Reason
}

// IsCloudAbsent reports whether err is a CloudAbsentError.
func IsCloudAbsent(err error) bool {
	_, ok := err.(*CloudAbsentError)
	return ok
}
