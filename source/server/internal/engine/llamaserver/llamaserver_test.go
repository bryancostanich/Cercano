package llamaserver

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
			"choices": [{"message": {"role": "assistant", "content": "hello from gguf"}}],
			"usage": {"prompt_tokens": 4, "completion_tokens": 3}
		}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:          "llama_server:model-a",
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
	if result.Output != "hello from gguf" || result.InputTokens != 4 || result.OutputTokens != 3 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if sawPath != "/v1/chat/completions" {
		t.Fatalf("unexpected path %q", sawPath)
	}
	if manager.startCount != 1 {
		t.Fatalf("expected runtime start, got %d", manager.startCount)
	}
	if sawPayload.Model != "llama_server:model-a" {
		t.Fatalf("payload model = %q", sawPayload.Model)
	}
	if len(sawPayload.Messages) != 2 || sawPayload.Messages[0].Role != "system" || sawPayload.Messages[1].Content != "hi" {
		t.Fatalf("unexpected messages: %#v", sawPayload.Messages)
	}
}

func TestCompleteStream_DecodesSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			ModelID:  "llama_server:model-a",
			State:    localruntime.StateRunning,
			Endpoint: server.URL,
		}},
		models: []localruntime.ModelRecord{{
			ID:          "llama_server:model-a",
			DisplayName: "Model A",
			Runtime:     runtimeName,
			Active:      true,
		}},
	}
	var tokens []string
	result, err := NewEngine(manager).CompleteStream(context.Background(), "", "hi", "", engine.GenOptions{}, func(token string) {
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
			ModelID:  "llama_server:model-a",
			State:    localruntime.StateRunning,
			Endpoint: server.URL,
		}},
		models: []localruntime.ModelRecord{{
			ID:          "llama_server:model-a",
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

type fakeRuntimeManager struct {
	models        []localruntime.ModelRecord
	instances     []localruntime.InstanceRecord
	startEndpoint string
	startCount    int
}

func (m *fakeRuntimeManager) RegisterProvider(localruntime.Provider) {}
func (m *fakeRuntimeManager) Providers() []localruntime.ProviderInfo { return nil }
func (m *fakeRuntimeManager) Inventory(context.Context) ([]localruntime.ModelRecord, error) {
	return m.models, nil
}
func (m *fakeRuntimeManager) Instances(context.Context) ([]localruntime.InstanceRecord, error) {
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
		ModelID:   "llama_server:model-a",
		State:     localruntime.StateRunning,
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
func (m *fakeRuntimeManager) WriteLog(localruntime.LogEntry) {}

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
			ID:          "llama_server:model-a",
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
