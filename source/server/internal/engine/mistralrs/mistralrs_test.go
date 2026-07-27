package mistralrs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/localruntime"
)

func TestComplete_StartsRuntimeAndCallsChatCompletions(t *testing.T) {
	var sawPath string
	var sawPayload chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&sawPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "hello from mistralrs"}}],
			"usage": {"prompt_tokens": 4, "completion_tokens": 3}
		}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:          "mistralrs:model-a",
			DisplayName: "Model A",
			Runtime:     runtimeName,
			Path:        "/models/model-a.gguf",
			Active:      true,
		}},
		startEndpoint: server.URL,
	}
	eng := NewEngine(manager)
	result, err := eng.Complete(context.Background(), "/models/model-a.gguf", "hi", "be brief", engine.GenOptions{})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if result.Output != "hello from mistralrs" || result.InputTokens != 4 || result.OutputTokens != 3 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if sawPath != "/v1/chat/completions" {
		t.Fatalf("unexpected path %q", sawPath)
	}
	if manager.startCount != 1 {
		t.Fatalf("expected runtime start, got %d", manager.startCount)
	}
	if sawPayload.Model != "default" {
		t.Fatalf("payload model = %q", sawPayload.Model)
	}
	if len(sawPayload.Messages) != 2 || sawPayload.Messages[0].Role != "system" || sawPayload.Messages[1].Content != "hi" {
		t.Fatalf("unexpected messages: %#v", sawPayload.Messages)
	}
	if sawPayload.MaxTokens != engine.DefaultMaxTokens {
		t.Fatalf("max_tokens = %d, want default %d", sawPayload.MaxTokens, engine.DefaultMaxTokens)
	}
}

func TestCompleteStream_DecodesSSE(t *testing.T) {
	var sawPayload chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sawPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}],\"timings\":{\"prompt_n\":2,\"predicted_n\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		instances: []localruntime.InstanceRecord{{
			ID:       "inst",
			Runtime:  runtimeName,
			ModelID:  "mistralrs:model-a",
			State:    localruntime.InstanceRunning,
			Endpoint: server.URL,
		}},
		models: []localruntime.ModelRecord{{
			ID:          "mistralrs:model-a",
			DisplayName: "Model A",
			Runtime:     runtimeName,
			Active:      true,
		}},
	}
	var tokens []string
	result, err := NewEngine(manager).CompleteStream(context.Background(), "", "hi", "", engine.GenOptions{MaxTokens: 123}, func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatalf("CompleteStream returned error: %v", err)
	}
	if result.Output != "hello" || strings.Join(tokens, "|") != "hel|lo" {
		t.Fatalf("unexpected stream output=%q tokens=%v", result.Output, tokens)
	}
	if result.InputTokens != 2 || result.OutputTokens != 5 {
		t.Fatalf("unexpected token counts: %#v", result)
	}
	if sawPayload.MaxTokens != 123 {
		t.Fatalf("max_tokens = %d, want explicit 123", sawPayload.MaxTokens)
	}
}

func TestChatWithTools_DecodesToolCalls(t *testing.T) {
	var sawPayload chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sawPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {"name": "read_file", "arguments": "{\"path\":\"README.md\"}"}
					}]
				}
			}],
			"timings": {"prompt_n": 7, "predicted_n": 2}
		}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		instances: []localruntime.InstanceRecord{{
			ID:       "inst",
			Runtime:  runtimeName,
			ModelID:  "mistralrs:model-a",
			State:    localruntime.InstanceRunning,
			Endpoint: server.URL,
		}},
		models: []localruntime.ModelRecord{{
			ID:          "mistralrs:model-a",
			DisplayName: "Model A",
			Runtime:     runtimeName,
			Active:      true,
		}},
	}
	resp, err := NewEngine(manager).ChatWithTools(context.Background(), engine.ChatRequest{
		Messages: []engine.ChatMessage{{Role: "user", Content: "read it"}},
		Tools: []engine.ToolSchemaJSON{{
			Type: "function",
			Function: engine.ToolFunctionJSON{
				Name:        "read_file",
				Description: "Read a file",
				Parameters:  map[string]interface{}{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatWithTools returned error: %v", err)
	}
	if !sawPayload.ParseToolCalls || !sawPayload.ParallelToolUse || sawPayload.ToolChoice != "auto" {
		t.Fatalf("tool flags not set: %#v", sawPayload)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool call: %#v", resp.ToolCalls[0])
	}
	if string(resp.ToolCalls[0].Function.Arguments) != `{"path":"README.md"}` {
		t.Fatalf("unexpected arguments: %s", resp.ToolCalls[0].Function.Arguments)
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 2 {
		t.Fatalf("unexpected token counts: %#v", resp)
	}
}

func TestComplete_TemperatureOption(t *testing.T) {
	var sawPayload chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decode into a fresh struct: reusing sawPayload would leave request 1's
		// Temperature pointer in place when request 2 omits the key entirely.
		var payload chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		sawPayload = payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "ok"}}]}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:          "mistralrs:model-a",
			DisplayName: "Model A",
			Runtime:     runtimeName,
			Path:        "/models/model-a.gguf",
			Active:      true,
		}},
		startEndpoint: server.URL,
	}
	eng := NewEngine(manager)

	if _, err := eng.Complete(context.Background(), "/models/model-a.gguf", "hi", "", engine.Greedy()); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if sawPayload.Temperature == nil || *sawPayload.Temperature != 0 {
		t.Fatalf("greedy request temperature = %v, want pointer to 0", sawPayload.Temperature)
	}

	if _, err := eng.Complete(context.Background(), "/models/model-a.gguf", "hi", "", engine.GenOptions{}); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if sawPayload.Temperature != nil {
		t.Fatalf("default request temperature = %v, want unset", *sawPayload.Temperature)
	}
}

func startingInstance(state localruntime.InstanceState, lastErr string) localruntime.InstanceRecord {
	return localruntime.InstanceRecord{
		ID:        "inst-1",
		Runtime:   runtimeName,
		ModelID:   "mistralrs:model-a",
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
			ID:          "mistralrs:model-a",
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
	endpoint, _, err := eng.endpointFor(ctx, "mistralrs:model-a")
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
			ID:      "mistralrs:model-a",
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
	_, _, err := eng.endpointFor(ctx, "mistralrs:model-a")
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v, want failure carrying the instance's LastError", err)
	}
	if manager.startCount != 0 {
		t.Fatalf("startCount = %d, want 0", manager.startCount)
	}
}

type fakeRuntimeManager struct {
	models        []localruntime.ModelRecord
	instances     []localruntime.InstanceRecord
	startEndpoint string
	startCount    int
	// onInstances, when set, overrides the instances list per call (1-based
	// call counter) — lets a test walk an instance through starting → running
	// the way the provider's background finishReadiness would.
	onInstances    func(call int) []localruntime.InstanceRecord
	instancesCalls int
}

func (m *fakeRuntimeManager) RegisterProvider(localruntime.Provider) {}
func (m *fakeRuntimeManager) Providers() []localruntime.ProviderInfo { return nil }
func (m *fakeRuntimeManager) Inventory(context.Context) ([]localruntime.ModelRecord, error) {
	return m.models, nil
}
func (m *fakeRuntimeManager) Instances(context.Context) ([]localruntime.InstanceRecord, error) {
	m.instancesCalls++
	if m.onInstances != nil {
		return m.onInstances(m.instancesCalls), nil
	}
	return m.instances, nil
}
func (m *fakeRuntimeManager) Endpoints(context.Context) ([]localruntime.EndpointRecord, error) {
	return nil, nil
}
func (m *fakeRuntimeManager) SetEndpoints([]localruntime.EndpointRecord) {}
func (m *fakeRuntimeManager) UpdateInstance(localruntime.InstanceRecord) {}
func (m *fakeRuntimeManager) Start(_ context.Context, req localruntime.StartRequest) (*localruntime.InstanceRecord, error) {
	m.startCount++
	instance := localruntime.InstanceRecord{
		ID:        "started",
		Runtime:   runtimeName,
		ModelID:   "mistralrs:model-a",
		State:     localruntime.InstanceRunning,
		Endpoint:  m.startEndpoint,
		StartedAt: time.Now(),
	}
	m.instances = append(m.instances, instance)
	return &instance, nil
}
func (m *fakeRuntimeManager) Stop(context.Context, localruntime.StopRequest) error { return nil }
func (m *fakeRuntimeManager) Restart(context.Context, localruntime.RestartRequest) (*localruntime.InstanceRecord, error) {
	return nil, nil
}
func (m *fakeRuntimeManager) DownloadModel(context.Context, localruntime.DownloadRequest) (*localruntime.ModelRecord, error) {
	return nil, nil
}
func (m *fakeRuntimeManager) EnsureModelsPresent(context.Context, string, []string) error {
	return nil
}
func (m *fakeRuntimeManager) ResolveOpenModel(context.Context, string, string) (localruntime.ModelRecord, error) {
	return localruntime.ModelRecord{}, nil
}
func (m *fakeRuntimeManager) CancelDownload(context.Context, localruntime.DownloadRequest) (*localruntime.ModelRecord, error) {
	return nil, nil
}
func (m *fakeRuntimeManager) DeleteModel(context.Context, localruntime.DeleteModelRequest) error {
	return nil
}
func (m *fakeRuntimeManager) Status(context.Context) (*localruntime.StatusSnapshot, error) {
	return nil, nil
}
func (m *fakeRuntimeManager) Logs(context.Context, localruntime.LogRequest) ([]localruntime.LogEntry, error) {
	return nil, nil
}
func (m *fakeRuntimeManager) WriteLog(localruntime.LogEntry)         {}
func (m *fakeRuntimeManager) RegisterObserver(localruntime.Observer) {}
