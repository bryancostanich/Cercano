package llamaserver

import (
	"context"
	"fmt"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/localruntime"
)

func confirmedInstance(i localruntime.InstanceRecord) bool {
	return (i.State == localruntime.InstanceRunning || i.State == localruntime.InstanceHealthy) && i.Context.ConfirmedTokens > 0 && !i.Context.ConfirmedAt.IsZero() && i.ID != ""
}

func (e *Engine) RuntimeContext(ctx context.Context, model string, prepare bool) (llm.RuntimeContext, error) {
	fail := func(err error) (llm.RuntimeContext, error) {
		return llm.RuntimeContext{}, &llm.LocalStartupError{Provider: runtimeName, Model: model, Err: err}
	}
	if e == nil || e.Manager == nil {
		return fail(fmt.Errorf("runtime manager unavailable"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	endpoint := ""
	if prepare {
		var err error
		endpoint, _, _, err = e.endpointFor(ctx, model)
		if err != nil {
			return llm.RuntimeContext{}, err
		}
	}
	models, err := e.Manager.Inventory(ctx)
	if err != nil {
		return fail(err)
	}
	selected := matchRuntimeModel(model, models)
	instances, err := e.Manager.Instances(ctx)
	if err != nil {
		return fail(err)
	}
	for _, i := range instances {
		matches := i.ModelID == model || (selected.ID != "" && i.ModelID == selected.ID)
		if i.Runtime != runtimeName || !matches || (endpoint != "" && i.Endpoint != endpoint) || !confirmedInstance(i) {
			continue
		}
		return llm.RuntimeContext{Window: i.Context.ConfirmedTokens, InstanceID: fmt.Sprintf("%s/%d/%d", i.ID, i.PID, i.StartedAt.UnixNano())}, nil
	}
	return fail(fmt.Errorf("no confirmed serving capacity for model %s", model))
}

func (p *LLMProvider) RuntimeContext(ctx context.Context, model string, prepare bool) (llm.RuntimeContext, error) {
	return p.eng.RuntimeContext(ctx, model, prepare)
}
