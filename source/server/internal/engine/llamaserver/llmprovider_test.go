package llamaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/failurelog"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/localruntime"
)

// TestLLMProviderChat drives the native-provider adapter end to end: resolve
// the warm instance for the requested model, POST /v1/chat/completions on its
// endpoint, translate the response into llm blocks.
func TestLLMProviderChat(t *testing.T) {
	var sawPath string
	var sawPayload struct {
		MaxTokens int `json:"max_tokens"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&sawPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "verdict: fine"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 7, "completion_tokens": 3}
		}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:           "llama_server:phi4",
			DisplayName:  "Phi 4 Mini Instruct",
			Runtime:      runtimeName,
			Path:         "/models/phi-4-mini-instruct.gguf",
			SupportsChat: true,
		}},
		instances: []localruntime.InstanceRecord{{
			ID:       "inst-chat",
			Runtime:  runtimeName,
			ModelID:  "llama_server:phi4",
			State:    localruntime.InstanceRunning,
			Endpoint: server.URL,
		}},
	}
	prov := NewLLMProvider(NewEngine(manager))

	// Resolution by display name — model tiers written from the settings UI
	// carry display names; the adapter must resolve them like every other
	// llama-server model reference.
	resp, err := prov.Chat(context.Background(), llm.ChatRequest{
		Model: "Phi 4 Mini Instruct",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "judge this"}}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if sawPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", sawPath)
	}
	if sawPayload.MaxTokens != engine.DefaultMaxTokens {
		t.Errorf("max_tokens = %d, want default %d", sawPayload.MaxTokens, engine.DefaultMaxTokens)
	}
	if len(resp.Blocks) == 0 || resp.Blocks[0].Text != "verdict: fine" {
		t.Errorf("blocks = %+v, want text 'verdict: fine'", resp.Blocks)
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 3 {
		t.Errorf("tokens = %d/%d, want 7/3", resp.InputTokens, resp.OutputTokens)
	}
}

func TestLLMProviderName(t *testing.T) {
	prov := NewLLMProvider(NewEngine(nil))
	if prov.Name() != "llama_server" {
		t.Errorf("Name() = %q", prov.Name())
	}
	if !prov.Capabilities().SupportsTools {
		t.Error("llama-server provider must advertise tool support")
	}
}

func TestLLMProviderLogsRichLocalRuntimeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":0,"state":1},{"id":1,"state":1},{"id":2,"state":1},{"id":3,"state":0}]`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"Context size has been exceeded"}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:           "llama_server:phi4",
			DisplayName:  "Phi 4 Mini Instruct",
			Runtime:      runtimeName,
			Path:         "/models/phi-4-mini-instruct.gguf",
			SupportsChat: true,
		}},
		instances: []localruntime.InstanceRecord{{
			ID:       "inst-chat",
			Runtime:  runtimeName,
			ModelID:  "llama_server:phi4",
			State:    localruntime.InstanceRunning,
			PID:      4242,
			Port:     8080,
			Endpoint: server.URL,
		}},
		logs: []localruntime.LogEntry{{
			Timestamp: time.Now(),
			Source:    "llama_server.inst.stderr",
			RuntimeID: "inst-chat",
			Level:     "error",
			Message:   "slot update failed",
		}},
	}
	eng := NewEngine(manager)
	eng.SetContextWindowResolver(func(string) int { return 8192 })
	logPath := filepath.Join(t.TempDir(), "failures.jsonl")
	w, err := failurelog.NewWriter(logPath)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()
	eng.SetFailureLog(w)
	prov := NewLLMProvider(eng)

	_, err = prov.StreamChat(context.Background(), llm.ChatRequest{
		ConversationID: "conv-local",
		RequestID:      "req-local",
		Model:          "Phi 4 Mini Instruct",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "small secret prompt"}}},
		},
		MaxTokens: 123,
	})
	if err == nil {
		t.Fatal("StreamChat succeeded; want llama-server error")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read failure log: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		`"event":"local_runtime.provider_error"`,
		`"provider":"llama_server"`,
		`"http_status":500`,
		`"error_class":"context_overflow"`,
		`"request_token_estimate":`,
		`"max_tokens":123`,
		`"context_window":8192`,
		`"instance_id":"inst-chat"`,
		`"pid":4242`,
		`"port":8080`,
		`"endpoint":"` + server.URL + `"`,
		`"slots":`,
		`"runtime_log_tail":`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("failure log missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "small secret prompt") {
		t.Fatalf("failure log included prompt text: %s", got)
	}
}
