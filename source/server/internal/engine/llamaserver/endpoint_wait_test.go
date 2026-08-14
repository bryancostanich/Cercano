package llamaserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/localruntime"
)

func TestMatchRuntimeModelPrefersExactCatalogRecord(t *testing.T) {
	requested := "llama_server:catalog:qwen2.5-vl-3b-instruct-q4_k_m"
	models := []localruntime.ModelRecord{
		{
			ID:             "llama_server:82a8bdf5aa27",
			DisplayName:    "Qwen2.5 VL 3B Instruct",
			Runtime:        runtimeName,
			Path:           "/models/qwen2.5-vl-3b-instruct-q4_k_m/Qwen2.5-VL-3B-Instruct-Q4_K_M.gguf",
			SupportsVision: false,
			DownloadState:  localruntime.Downloaded,
		},
		{
			ID:             requested,
			DisplayName:    "Qwen2.5-VL-3B Instruct Q4_K_M",
			Runtime:        runtimeName,
			Path:           "/models/qwen2.5-vl-3b-instruct-q4_k_m/Qwen2.5-VL-3B-Instruct-Q4_K_M.gguf",
			SupportsVision: true,
			MmprojPath:     "/models/qwen2.5-vl-3b-instruct-q4_k_m/mmproj-Qwen2.5-VL-3B-Instruct-Q8_0.gguf",
			DownloadState:  localruntime.Downloaded,
		},
	}

	got := matchRuntimeModel(requested, models)
	if got.ID != requested {
		t.Fatalf("matchRuntimeModel chose %q, want exact catalog record %q", got.ID, requested)
	}
	if !got.SupportsVision || got.MmprojPath == "" {
		t.Fatalf("catalog metadata was not preserved: %+v", got)
	}
}

// TestEndpointFor_StartsWithResolvedCatalogID guards the vision-model launch
// path: a bare tier override (e.g. "gemma-3-4b-it-q4_k_m") must resolve to the
// catalog record and be started by its catalog ID, so the manager launches with
// the catalog's MmprojPath (--mmproj) instead of re-resolving to a
// path-discovered record that would drop the projector and run text-only.
func TestEndpointFor_StartsWithResolvedCatalogID(t *testing.T) {
	catalogID := runtimeName + ":catalog:gemma-3-4b-it-q4_k_m"
	manager := &fakeRuntimeManager{
		startEndpoint: "http://127.0.0.1:4243",
		models: []localruntime.ModelRecord{
			{
				ID:            runtimeName + ":7dad57faada8",
				DisplayName:   "gemma-3-4b-it-Q4_K_M",
				Runtime:       runtimeName,
				Path:          "/models/gemma-3-4b-it-q4_k_m/gemma-3-4b-it-Q4_K_M.gguf",
				DownloadState: localruntime.Downloaded,
			},
			{
				ID:             catalogID,
				DisplayName:    "Gemma 3 4B Instruct Q4_K_M",
				Runtime:        runtimeName,
				Path:           "/models/gemma-3-4b-it-q4_k_m/gemma-3-4b-it-Q4_K_M.gguf",
				SupportsVision: true,
				MmprojPath:     "/models/gemma-3-4b-it-q4_k_m/mmproj-model-f16.gguf",
				DownloadState:  localruntime.Downloaded,
			},
		},
	}
	e := &Engine{Manager: manager}

	endpoint, _, supportsVision, err := e.endpointFor(context.Background(), "gemma-3-4b-it-q4_k_m")
	if err != nil {
		t.Fatalf("endpointFor: %v", err)
	}
	if endpoint != "http://127.0.0.1:4243" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if !supportsVision {
		t.Fatal("expected supportsVision true from catalog record")
	}
	if manager.startModelID != catalogID {
		t.Fatalf("Start ModelID = %q, want resolved catalog ID %q", manager.startModelID, catalogID)
	}
}

func startingInstance(state localruntime.InstanceState, lastErr string) localruntime.InstanceRecord {
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
				return []localruntime.InstanceRecord{startingInstance(localruntime.InstanceStarting, "")}
			}
			return []localruntime.InstanceRecord{startingInstance(localruntime.InstanceRunning, "")}
		},
	}
	eng := NewEngine(manager)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint, _, _, err := eng.endpointFor(ctx, "llama_server:model-a")
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
				return []localruntime.InstanceRecord{startingInstance(localruntime.InstanceStarting, "")}
			}
			return []localruntime.InstanceRecord{startingInstance(localruntime.InstanceFailed, "exit status 1")}
		},
	}
	eng := NewEngine(manager)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _, err := eng.endpointFor(ctx, "llama_server:model-a")
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
			return []localruntime.InstanceRecord{startingInstance(localruntime.InstanceStarting, "")}
		},
	}
	eng := NewEngine(manager)

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	_, _, _, err := eng.endpointFor(ctx, "llama_server:model-a")
	if err == nil || !strings.Contains(err.Error(), "still loading") {
		t.Fatalf("err = %v, want a still-loading error", err)
	}
	if manager.startCount != 0 {
		t.Fatalf("startCount = %d, want 0", manager.startCount)
	}
}
