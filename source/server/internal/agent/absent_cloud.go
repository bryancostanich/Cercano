package agent

import "context"

// AbsentCloudProvider is the runtime stand-in for "no real cloud configured".
// Every Process call returns a CloudAbsentError, which the Agent intercepts to
// auto-degrade the turn to the local provider with a visible notice.
//
// This replaces silent mock responses in the runtime path — pretending a cloud
// answered is a trust violation.
type AbsentCloudProvider struct {
	reason string
}

// AbsentCloud returns a sentinel cloud turn runner that always errors with
// CloudAbsentError. reason is shown to the user in the degrade notice.
func AbsentCloud(reason string) *AbsentCloudProvider {
	if reason == "" {
		reason = "no API key or proxy base URL configured"
	}
	return &AbsentCloudProvider{reason: reason}
}

// Process always returns a CloudAbsentError carrying the configured reason.
func (a *AbsentCloudProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return nil, &CloudAbsentError{Reason: a.reason}
}

// Name returns the sentinel name used in status displays.
func (a *AbsentCloudProvider) Name() string { return "NONE" }

// Reason exposes the configured human-readable reason for absence.
func (a *AbsentCloudProvider) Reason() string { return a.reason }
