package anthropic

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

const sseFixture = `event: message_start
data: {"type":"message_start","message":{"id":"m_1","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"u1","name":"read_file","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"x\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}

`

func TestStreamChat_EmitsExpectedEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(sseFixture))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Model: "claude"})
	rdr, err := c.StreamChat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer rdr.Close()

	got := []llm.StreamEventType{}
	for {
		ev, ok, err := rdr.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		got = append(got, ev.Type)
	}
	want := []llm.StreamEventType{
		llm.EventMessageStart,
		llm.EventTextDelta,
		llm.EventToolUseStart,
		llm.EventToolUseInputDelta,
		llm.EventToolUseStop,
		llm.EventMessageStop,
	}
	if len(got) < len(want) {
		t.Fatalf("got %v, want at least %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("event %d: got %s want %s", i, got[i], w)
		}
	}
}
