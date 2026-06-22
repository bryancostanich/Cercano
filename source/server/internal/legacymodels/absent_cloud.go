package legacymodels

import (
	"context"

	"cercano/source/server/internal/agent"
)

// AbsentCloudProvider is the runtime stand-in for "no real cloud configured".
// Every Process call returns a CloudAbsentError, which the Agent intercepts
// to auto-degrade the turn to the local provider with a visible notice.
//
// This replaces the legacy MockProvider in the runtime path — silent mocked
// responses are a trust violation per the CLI design doc.
type AbsentCloudProvider struct {
	reason string
}

// NewAbsentCloudProvider returns a sentinel cloud provider that always errors
// with CloudAbsentError. `reason` is shown to the user in the degrade notice.
func NewAbsentCloudProvider(reason string) *AbsentCloudProvider {
	if reason == "" {
		reason = "no API key or proxy base URL configured"
	}
	return &AbsentCloudProvider{reason: reason}
}

// Process always returns a CloudAbsentError carrying the configured reason.
func (a *AbsentCloudProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	return nil, &agent.CloudAbsentError{Reason: a.reason}
}

// Name returns the sentinel name used in status displays.
func (a *AbsentCloudProvider) Name() string { return "NONE" }

// Reason exposes the configured human-readable reason for absence.
func (a *AbsentCloudProvider) Reason() string { return a.reason }
