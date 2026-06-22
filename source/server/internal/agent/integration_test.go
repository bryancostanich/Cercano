package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm/anthropic"
)

// SSE fixture: first round emits "Listing." text + tool_use(LS, path=".")
const sseRound1 = `event: message_start
data: {"type":"message_start","message":{"id":"m_1","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Listing."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"u1","name":"LS","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\".\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

// SSE fixture: second round emits "Done." and ends turn.
const sseRound2 = `event: message_start
data: {"type":"message_start","message":{"id":"m_2","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"usage":{"input_tokens":20,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`

func TestIntegration_FullTurnWithToolCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if calls == 1 {
			_, _ = w.Write([]byte(sseRound1))
		} else {
			_, _ = w.Write([]byte(sseRound2))
		}
	}))
	defer srv.Close()

	prov := anthropic.NewClient(anthropic.Config{
		BaseURL: srv.URL, APIKey: "dummy", Model: "claude",
	})
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Model: "claude", UserInput: "list this dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected 2 round-trips, got %d", calls)
	}
	if result.FinalText != "Done." {
		t.Errorf("final text: %q", result.FinalText)
	}
}
