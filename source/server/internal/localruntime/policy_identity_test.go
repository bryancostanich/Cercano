package localruntime

import (
	"context"
	"errors"
	"testing"
)

type policyRuntime struct {
	Manager
	instances []InstanceRecord
	err       error
}

func (m *policyRuntime) Instances(context.Context) ([]InstanceRecord, error) {
	return m.instances, m.err
}

func TestPolicyIdentityFollowsLoadedModel(t *testing.T) {
	manager := &policyRuntime{instances: []InstanceRecord{{Runtime: "mistralrs", Endpoint: "http://127.0.0.1:1234", ModelID: "first", State: InstanceRunning}}}
	resolve := func() (string, error) {
		return ModelAtEndpoint(context.Background(), manager, "mistralrs", "http://127.0.0.1:1234/v1")
	}
	if model, err := resolve(); err != nil || model != "first" {
		t.Fatalf("model=%q err=%v", model, err)
	}
	manager.instances[0].ModelID = "replacement"
	if model, err := resolve(); err != nil || model != "replacement" {
		t.Fatalf("stale model=%q err=%v", model, err)
	}
	manager.instances = append(manager.instances, InstanceRecord{Runtime: "mistralrs", Endpoint: "http://127.0.0.1:1234", ModelID: "other", State: InstanceRunning})
	if _, err := resolve(); err == nil {
		t.Fatal("ambiguous live model accepted")
	}
	manager.instances = manager.instances[:1]
	manager.instances[0].State = InstanceStopped
	if _, err := resolve(); err == nil {
		t.Fatal("stopped model accepted")
	}
	manager.instances[0].State = InstanceRunning
	manager.instances[0].Endpoint = "http://127.0.0.1:5678"
	if _, err := resolve(); err == nil {
		t.Fatal("another endpoint accepted")
	}
	manager.err = errors.New("inventory unavailable")
	if _, err := resolve(); err == nil {
		t.Fatal("unavailable runtime accepted")
	}
}
