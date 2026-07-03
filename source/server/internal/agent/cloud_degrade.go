package agent

import (
	"context"
	"fmt"
)

// degradeIfCloudFailure handles the "cloud was selected but didn't work" case:
// any non-nil error returned by a non-local provider is treated as a cloud
// failure (absent provider, auth, network, model name, rate limit, etc.) and
// causes the turn to retry against the local provider with a human-readable
// notice attached. Returns (localResponse, true) if the retry succeeded;
// (nil, false) if no retry was attempted or the caller should propagate the
// original error.
//
// Streaming-aware: if a non-nil ProgressFunc is supplied, it gets a
// "cloud … — degrading to local" message. Token streaming is not retried
// (the per-token channel belongs to the original call); the response Output
// field is filled by the local retry's blocking Process call.
func (a *Agent) degradeIfCloudFailure(ctx context.Context, provider ModelProvider, req *Request, originalErr error, progress ProgressFunc) (*Response, bool) {
	if originalErr == nil {
		return nil, false
	}
	local, ok := a.router.GetModelProviders()["OpenModel"]
	if !ok || provider == local {
		return nil, false
	}

	kind := "failed"
	reason := originalErr.Error()
	if IsCloudAbsent(originalErr) {
		kind = "not configured"
		reason = originalErr.(*CloudAbsentError).Reason
	}
	fmt.Printf("Agent: cloud route %s (%s) — degrading to local.\n", kind, reason)
	if progress != nil {
		progress("cloud " + kind + " — degrading to local")
	}

	res, err := local.Process(ctx, req)
	if err != nil {
		// Local also failed — return the original cloud error to the caller
		// since that's the more useful signal.
		fmt.Printf("Agent: local fallback also failed: %v\n", err)
		return nil, false
	}
	hint := "Check the agent log."
	if kind == "not configured" {
		hint = "Set cloud_base_url / cloud_api_key in ~/.config/cercano/config.yaml to enable cloud."
	} else {
		hint = "Check Meridian / API key / model name in ~/.config/cercano/config.yaml. Tail the agent log for the full error."
	}
	res.Notice = "Cloud " + kind + " (" + reason + "). Answered locally. " + hint
	return res, true
}
