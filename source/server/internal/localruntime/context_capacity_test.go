package localruntime

import (
	"context"
	"testing"
	"time"
)

func TestManagerInvalidatesNonServingCapacity(t *testing.T) {
	for _, state := range []InstanceState{InstanceStarting, InstanceStopped, InstanceFailed} {
		t.Run(state.String(), func(t *testing.T) {
			m := NewManager()
			m.UpdateInstance(InstanceRecord{ID: "instance", ModelID: "model", Runtime: "llama_server", State: state, Context: ContextCapacity{PlannedTokens: 65536, PlannedSource: "config", ConfirmedTokens: 8192, ConfirmedAt: time.Now()}})
			instances, err := m.Instances(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(instances) != 1 {
				t.Fatalf("instances=%v", instances)
			}
			c := instances[0].Context
			if c.ConfirmedTokens != 0 || !c.ConfirmedAt.IsZero() {
				t.Fatal("non-serving instance retained confirmed capacity")
			}
			if c.PlannedTokens != 65536 || c.PlannedSource != "config" {
				t.Fatal("lost planned allocation")
			}
		})
	}
}
