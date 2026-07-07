package llamaserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/localruntime"
)

func startingInstance(state, lastErr string) localruntime.InstanceRecord {
	return localruntime.InstanceRecord{
		ID:        "inst-1",
		Runtime:   runtimeName,
		ModelID:   "llama_server:model-a",
		State:     state,
		Endpoint:  "http://127.0.0.1:4242",
		LastError: lastErr,
		StartedAt: time.Now(),
	}
}

// TestEndpointFor_WaitsForStartingInstance drives the warm-reuse path: a
// still-loading instance exists, so endpointFor must wait for it to become
// running and reuse it — never spawn a second copy of the model.
func TestEndpointFor_WaitsForStartingInstance(t *testing.T) {
	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:          "llama_server:model-a",
			DisplayName: "model-a",
			Runtime:     runtimeName,
			Path:        "/models/model-a.gguf",
		}},
		onInstances: func(call int) []localruntime.InstanceRecord {
			if call <= 1 {
				return []localruntime.InstanceRecord{startingInstance(localruntime.StateStarting, "")}
			}
			return []localruntime.InstanceRecord{startingInstance(localruntime.StateRunning, "")}
		},
	}
	eng := NewEngine(manager)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint, _, err := eng.endpointFor(ctx, "llama_server:model-a")
	if err != nil {
		t.Fatalf("endpointFor: %v", err)
	}
	if endpoint != "http://127.0.0.1:4242" {
		t.Fatalf("endpoint = %q, want the starting instance's endpoint", endpoint)
	}
	if manager.startCount != 0 {
		t.Fatalf("startCount = %d — waited request must reuse the loading instance, not spawn another", manager.startCount)
	}
}

// TestEndpointFor_StartingInstanceFails surfaces a load failure to the caller
// with the instance's recorded error instead of hanging until deadline.
func TestEndpointFor_StartingInstanceFails(t *testing.T) {
	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:      "llama_server:model-a",
			Runtime: runtimeName,
			Path:    "/models/model-a.gguf",
		}},
		onInstances: func(call int) []localruntime.InstanceRecord {
			if call <= 1 {
				return []localruntime.InstanceRecord{startingInstance(localruntime.StateStarting, "")}
			}
			return []localruntime.InstanceRecord{startingInstance(localruntime.StateFailed, "exit status 1")}
		},
	}
	eng := NewEngine(manager)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := eng.endpointFor(ctx, "llama_server:model-a")
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v, want failure carrying the instance's LastError", err)
	}
	if manager.startCount != 0 {
		t.Fatalf("startCount = %d, want 0", manager.startCount)
	}
}

// TestEndpointFor_CtxExpiresWhileLoading bounds the wait by the caller's
// context: a model that loads slower than the caller can wait yields a
// "still loading" error, and the instance is left alone for later callers.
func TestEndpointFor_CtxExpiresWhileLoading(t *testing.T) {
	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:      "llama_server:model-a",
			Runtime: runtimeName,
			Path:    "/models/model-a.gguf",
		}},
		onInstances: func(int) []localruntime.InstanceRecord {
			return []localruntime.InstanceRecord{startingInstance(localruntime.StateStarting, "")}
		},
	}
	eng := NewEngine(manager)

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	_, _, err := eng.endpointFor(ctx, "llama_server:model-a")
	if err == nil || !strings.Contains(err.Error(), "still loading") {
		t.Fatalf("err = %v, want a still-loading error", err)
	}
	if manager.startCount != 0 {
		t.Fatalf("startCount = %d, want 0", manager.startCount)
	}
}
