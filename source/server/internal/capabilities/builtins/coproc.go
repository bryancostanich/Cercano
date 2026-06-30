package builtins

import (
	"context"
	"errors"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/tokens"
)

// runCoproc runs a fixed co-processor prompt through the one-shot dispatch engine,
// recording usage (with the cloud-tokens-saved metric) at the provider boundary.
// source is the per-tool telemetry label (e.g. "summarize"); content is the raw
// input used to estimate tokens avoided.
func runCoproc(ctx context.Context, call *capabilities.Call, source, prompt, content string) (*capabilities.Result, error) {
	if call.Svc.Dispatch == nil {
		return nil, errors.New("co-processor engine not available")
	}
	res, err := call.Svc.Dispatch(ctx, dispatch.Spec{
		Mode:                 dispatch.OneShot,
		Role:                 dispatch.RoleCoproc,
		Prompt:               prompt,
		WorkDir:              call.WorkDir,
		WantsProjectContext:  true,
		Source:               source,
		ContentTokensAvoided: tokens.Estimate(content),
		RecordUsage:          true,
	})
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(res.Text), nil
}
