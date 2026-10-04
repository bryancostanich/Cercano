package mistralrs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/modelpolicy"
)

func TestDirectEngineCannotBypassManagedPolicy(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	const modelID = "mistralrs:catalog:approved"
	manager := &fakeRuntimeManager{
		models:    []localruntime.ModelRecord{{ID: modelID, Runtime: runtimeName, Active: true}},
		instances: []localruntime.InstanceRecord{{ID: "instance", ModelID: modelID, Runtime: runtimeName, Endpoint: server.URL, State: localruntime.InstanceRunning}},
	}
	eng := NewEngine(manager)
	var checked atomic.Int32
	ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
		checked.Add(1)
		if a.Model != modelID || a.Endpoint != server.URL+"/v1" || a.Provider != "mistralrs" || a.Placement != "local" {
			t.Errorf("incorrect physical model: %+v", a)
		}
		return modelpolicy.Deny(a, "not approved")
	}))
	calls := []func() error{
		func() error { _, err := eng.Complete(ctx, modelID, "prompt", "", engine.GenOptions{}); return err },
		func() error {
			_, err := eng.CompleteStream(ctx, modelID, "prompt", "", engine.GenOptions{}, func(string) {})
			return err
		},
		func() error {
			_, err := eng.ChatWithTools(ctx, engine.ChatRequest{Model: modelID, Messages: []engine.ChatMessage{{Role: "user", Content: "prompt"}}})
			return err
		},
	}
	for _, call := range calls {
		if err := call(); llm.ClassOf(err) != llm.ErrPermission {
			t.Fatalf("incorrect denial: %v", err)
		}
	}
	if hits.Load() != 0 || checked.Load() != 3 {
		t.Fatalf("hits=%d checks=%d", hits.Load(), checked.Load())
	}
}
