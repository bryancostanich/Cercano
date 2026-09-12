package inference

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
	"context"
)

// WithTaskAssignment keeps main-loop quality tied to the same immutable routing
// graph that selected its provider, even while a settings rebuild is in progress.
func WithTaskAssignment(provider Provider, task config.Task, assignment config.TaskAssignment, model ...string) Provider {
	p := &assignedProvider{Provider: provider, task: task, assignment: assignment}
	if len(model) > 0 {
		p.model = model[0]
	}
	return p
}

type assignedProvider struct {
	Provider
	task       config.Task
	assignment config.TaskAssignment
	model      string
}

func (p *assignedProvider) TaskAssignmentFor(task config.Task) (config.TaskAssignment, bool) {
	return p.assignment, task == p.task
}
func (p *assignedProvider) TargetFor(model, tier string) llm.ServingRoute {
	return TargetFor(p.Provider, model, tier)
}
func (p *assignedProvider) TargetForCall(req Call) llm.ServingRoute {
	return TargetForCall(p.Provider, req)
}
func TaskAssignmentFor(provider Provider, task config.Task) (config.TaskAssignment, bool) {
	if scoped, ok := provider.(interface {
		TaskAssignmentFor(config.Task) (config.TaskAssignment, bool)
	}); ok {
		return scoped.TaskAssignmentFor(task)
	}
	return config.TaskAssignment{}, false
}

func (p *assignedProvider) TaskModelFor() (string, bool) { return p.model, p.model != "" }
func TaskModelFor(provider Provider) (string, bool) {
	if scoped, ok := provider.(interface{ TaskModelFor() (string, bool) }); ok {
		return scoped.TaskModelFor()
	}
	return "", false
}

func (p *assignedProvider) TargetForContext(ctx context.Context, req Call) llm.ServingRoute {
	return TargetForContext(ctx, p.Provider, req)
}

func (p *assignedProvider) RuntimeContext(ctx context.Context, model string, prepare bool) (llm.RuntimeContext, error) {
	return llm.ResolveRuntimeContext(ctx, p.Provider, model, prepare)
}
