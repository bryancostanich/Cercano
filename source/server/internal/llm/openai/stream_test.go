package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

// sse writes SSE-formatted lines to w (each line gets "data: <line>\n\n").
func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, l := range lines {
		fmt.Fprintf(w, "data: %s\n\n", l)
	}
}

// TestStreamChat_ToolDeltaEvents verifies the streamReader emits the correct
// START→DELTA→STOP event sequence mirroring the Anthropic contract:
//
//   - EventMessageStart at stream open
//   - EventTextDelta for content fragments
//   - EventToolUseStart when a tool-call index is first seen
//   - EventToolUseInputDelta (TextDelta = JSON fragment) for each argument chunk
//   - EventToolUseStop when the tool index closes
//   - EventMessageStop with InputTokens + OutputTokens from the usage chunk
//
// The httptest server splits tool-call arguments across two chunks to exercise
// the fragment-streaming path.
func TestStreamChat_ToolDeltaEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			// chunk 1: text content
			`{"choices":[{"delta":{"content":"hi "}}]}`,
			// chunk 2: first tool-call fragment (index 0, id, name, partial args)
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"p\":"}}]}}]}`,
			// chunk 3: second tool-call fragment (same index, rest of args)
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
			// chunk 4: finish reason + usage (stream_options include_usage)
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`,
			// SSE terminator
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	var (
		text        string
		toolName    string
		toolID      string
		toolArgs    string // reassembled from EventToolUseInputDelta fragments
		sawToolStop bool
		sawMsgStart bool
		inputToks   int
		outputToks  int
	)

	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventMessageStart:
			sawMsgStart = true
		case llm.EventTextDelta:
			text += ev.TextDelta
		case llm.EventToolUseStart:
			toolName = ev.ToolName
			toolID = ev.ToolUseID
		case llm.EventToolUseInputDelta:
			toolArgs += ev.TextDelta
		case llm.EventToolUseStop:
			sawToolStop = true
		case llm.EventMessageStop:
			inputToks = ev.InputTokens
			outputToks = ev.OutputTokens
		}
	}

	if !sawMsgStart {
		t.Fatal("expected EventMessageStart")
	}
	if text != "hi " {
		t.Fatalf("text: got %q, want %q", text, "hi ")
	}
	if toolName != "read" {
		t.Fatalf("toolName: got %q, want %q", toolName, "read")
	}
	if toolID != "c1" {
		t.Fatalf("toolID: got %q, want %q", toolID, "c1")
	}
	if toolArgs != `{"p":"x"}` {
		t.Fatalf("toolArgs (reassembled): got %q, want %q", toolArgs, `{"p":"x"}`)
	}
	if !sawToolStop {
		t.Fatal("expected EventToolUseStop")
	}
	if inputToks != 4 || outputToks != 6 {
		t.Fatalf("tokens on EventMessageStop: got input=%d output=%d, want 4/6", inputToks, outputToks)
	}
}

// TestStreamChat_SeparateUsageChunk verifies that the real OpenAI include_usage
// shape — where finish_reason and usage arrive in SEPARATE chunks — still
// produces the correct InputTokens/OutputTokens on EventMessageStop.
//
// Wire shape:
//  1. text delta chunk
//  2. chunk with choices[0].finish_reason="stop" and empty delta  (no usage yet)
//  3. chunk with choices=[] and usage object                       (len==0 path)
//  4. [DONE]
//
// The reader must skip the empty-choices chunk (continue) but still capture the
// usage before emitting EventMessageStop at EOF.
func TestStreamChat_SeparateUsageChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			// chunk 1: text content
			`{"choices":[{"delta":{"content":"hello"}}]}`,
			// chunk 2: finish_reason only — no usage yet
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			// chunk 3: usage only — choices array is empty (the len==0 continue path)
			`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`,
			// SSE terminator
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	var (
		text        string
		sawMsgStart bool
		stopReason  string
		inputToks   int
		outputToks  int
	)

	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventMessageStart:
			sawMsgStart = true
		case llm.EventTextDelta:
			text += ev.TextDelta
		case llm.EventMessageStop:
			stopReason = ev.StopReason
			inputToks = ev.InputTokens
			outputToks = ev.OutputTokens
		}
	}

	if !sawMsgStart {
		t.Fatal("expected EventMessageStart")
	}
	if text != "hello" {
		t.Fatalf("text: got %q, want %q", text, "hello")
	}
	if stopReason != "stop" {
		t.Fatalf("stopReason: got %q, want %q", stopReason, "stop")
	}
	if inputToks != 7 || outputToks != 3 {
		t.Fatalf("tokens on EventMessageStop: got input=%d output=%d, want 7/3", inputToks, outputToks)
	}
}
