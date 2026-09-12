package inference

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
	"context"
	"testing"
)

type runtimeAssignmentProbe struct {
	Provider
	model    string
	prepared bool
}

func (*runtimeAssignmentProbe) Name() string { return "llama_server" }
func (p *runtimeAssignmentProbe) RuntimeContext(_ context.Context, model string, prepare bool) (llm.RuntimeContext, error) {
	p.model = model
	p.prepared = prepare
	return llm.RuntimeContext{Window: 32768, InstanceID: "observed-instance"}, nil
}
func TestTaskAssignmentWrapperPreservesConfirmedRuntimeCapacity(t *testing.T) {
	underlying := &runtimeAssignmentProbe{}
	provider := WithTaskAssignment(underlying, config.TaskChat, config.TaskAssignment{Destination: config.DestinationLocal, Quality: config.CostPremium}, "local-model")
	observed, err := llm.ResolveRuntimeContext(context.Background(), provider, "local-model", true)
	if err != nil || observed.Window != 32768 || observed.InstanceID != "observed-instance" || !underlying.prepared || underlying.model != "local-model" {
		t.Fatalf("runtime confirmation lost: %+v %v", observed, err)
	}
}
