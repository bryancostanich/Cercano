package builtins

import (
	"context"
	"errors"

	"cercano/source/server/internal/capabilities"
)

// runCoproc runs a fixed co-processor prompt through the shared one-shot engine
// (via Services.RunCoproc) and returns the text result. Shared by the
// summarize/extract/classify/explain capabilities.
func runCoproc(ctx context.Context, call *capabilities.Call, prompt string) (*capabilities.Result, error) {
	if call.Svc.RunCoproc == nil {
		return nil, errors.New("co-processor engine not available")
	}
	out, err := call.Svc.RunCoproc(ctx, prompt, call.WorkDir)
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(out), nil
}
